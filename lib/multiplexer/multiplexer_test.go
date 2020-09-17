/*
Copyright 2017 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package multiplexer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gravitational/teleport/lib/fixtures"
	"github.com/gravitational/teleport/lib/httplib"
	"github.com/gravitational/teleport/lib/multiplexer/test"
	"github.com/gravitational/teleport/lib/sshutils"
	"github.com/gravitational/teleport/lib/utils"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/stretchr/testify/assert"
)

// TestMux tests multiplexing protocols
// using the same listener.
func TestMux(t *testing.T) {
	utils.InitLoggerForTests(testing.Verbose())

	_, signer, err := utils.CreateCertificate("foo", ssh.HostCert)
	assert.Nil(t, err)

	// TestMux tests basic use case of multiplexing TLS
	// and SSH on the same listener socket
	t.Run("TLSSSH", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: true,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		backend1 := &httptest.Server{
			Listener: mux.TLS(),
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "backend 1")
			}),
			},
		}
		backend1.StartTLS()
		defer backend1.Close()

		called := false
		sshHandler := sshutils.NewChanHandlerFunc(func(_ context.Context, _ *sshutils.ConnectionContext, nch ssh.NewChannel) {
			called = true
			err := nch.Reject(ssh.Prohibited, "nothing to see here")
			assert.Nil(t, err)
		})

		srv, err := sshutils.NewServer(
			"test",
			utils.NetAddr{AddrNetwork: "tcp", Addr: "localhost:0"},
			sshHandler,
			[]ssh.Signer{signer},
			sshutils.AuthMethods{Password: pass("abc123")},
		)
		assert.Nil(t, err)
		go srv.Serve(mux.SSH())
		defer srv.Close()
		clt, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			Auth:            []ssh.AuthMethod{ssh.Password("abc123")},
			Timeout:         time.Second,
			HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		})
		assert.Nil(t, err)
		defer clt.Close()

		// call new session to initiate opening new channel
		_, err = clt.NewSession()
		assert.NotNil(t, err)
		// make sure the channel handler was called OK
		assert.Equal(t, called, true)

		client := testClient(backend1)
		re, err := client.Get(backend1.URL)
		assert.Nil(t, err)
		defer re.Body.Close()
		bytes, err := ioutil.ReadAll(re.Body)
		assert.Nil(t, err)
		assert.Equal(t, string(bytes), "backend 1")

		// Close mux, new requests should fail
		mux.Close()
		mux.Wait()

		// use new client to use new connection pool
		client = testClient(backend1)
		re, err = client.Get(backend1.URL)
		if err == nil {
			re.Body.Close()
		}
		assert.NotNil(t, err)
	})

	// ProxyLine tests proxy line protocol
	t.Run("ProxyLine", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: true,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		backend1 := &httptest.Server{
			Listener: mux.TLS(),
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, r.RemoteAddr)
			}),
			},
		}
		backend1.StartTLS()
		defer backend1.Close()

		remoteAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8000}
		proxyLine := ProxyLine{
			Protocol:    TCP4,
			Source:      remoteAddr,
			Destination: net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9000},
		}

		parsedURL, err := url.Parse(backend1.URL)
		assert.Nil(t, err)

		conn, err := net.Dial("tcp", parsedURL.Host)
		assert.Nil(t, err)
		defer conn.Close()
		// send proxy line first before establishing TLS connection
		_, err = fmt.Fprint(conn, proxyLine.String())
		assert.Nil(t, err)

		// upgrade connection to TLS
		tlsConn := tls.Client(conn, clientConfig(backend1))
		defer tlsConn.Close()

		// make sure the TLS call succeeded and we got remote address
		// correctly
		out, err := utils.RoundtripWithConn(tlsConn)
		assert.Nil(t, err)
		assert.Equal(t, out, remoteAddr.String())
	})

	// TestDisabledProxy makes sure the connection gets dropped
	// when Proxy line support protocol is turned off
	t.Run("DisabledProxy", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: false,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		backend1 := &httptest.Server{
			Listener: mux.TLS(),
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, r.RemoteAddr)
			}),
			},
		}
		backend1.StartTLS()
		defer backend1.Close()

		remoteAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8000}
		proxyLine := ProxyLine{
			Protocol:    TCP4,
			Source:      remoteAddr,
			Destination: net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9000},
		}

		parsedURL, err := url.Parse(backend1.URL)
		assert.Nil(t, err)

		conn, err := net.Dial("tcp", parsedURL.Host)
		assert.Nil(t, err)
		defer conn.Close()
		// send proxy line first before establishing TLS connection
		_, err = fmt.Fprint(conn, proxyLine.String())
		assert.Nil(t, err)

		// upgrade connection to TLS
		tlsConn := tls.Client(conn, clientConfig(backend1))
		defer tlsConn.Close()

		// make sure the TLS call failed
		_, err = utils.RoundtripWithConn(tlsConn)
		assert.NotNil(t, err)
	})

	// Timeout tests client timeout - client dials, but writes nothing
	// make sure server hangs up
	t.Run("Timeout", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		config := Config{
			Listener:            listener,
			ReadDeadline:        time.Millisecond,
			EnableProxyProtocol: true,
		}
		mux, err := New(config)
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		backend1 := &httptest.Server{
			Listener: mux.TLS(),
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, r.RemoteAddr)
			}),
			},
		}
		backend1.StartTLS()
		defer backend1.Close()

		parsedURL, err := url.Parse(backend1.URL)
		assert.Nil(t, err)

		conn, err := net.Dial("tcp", parsedURL.Host)
		assert.Nil(t, err)
		defer conn.Close()

		time.Sleep(config.ReadDeadline + 5*time.Millisecond)
		// upgrade connection to TLS
		tlsConn := tls.Client(conn, clientConfig(backend1))
		defer tlsConn.Close()

		// roundtrip should fail on the timeout
		_, err = utils.RoundtripWithConn(tlsConn)
		assert.NotNil(t, err)
	})

	// UnknownProtocol make sure that multiplexer closes connection
	// with unknown protocol
	t.Run("UnknownProtocol", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: true,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		conn, err := net.Dial("tcp", listener.Addr().String())
		assert.Nil(t, err)
		defer conn.Close()

		// try plain HTTP
		_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
		assert.Nil(t, err)

		// connection should be closed
		_, err = conn.Read(make([]byte, 1))
		assert.Equal(t, err, io.EOF)
	})

	// DisableSSH disables SSH
	t.Run("DisableSSH", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: true,
			DisableSSH:          true,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		backend1 := &httptest.Server{
			Listener: mux.TLS(),
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "backend 1")
			}),
			},
		}
		backend1.StartTLS()
		defer backend1.Close()

		_, err = ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			Auth:            []ssh.AuthMethod{ssh.Password("abc123")},
			Timeout:         time.Second,
			HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		})
		assert.NotNil(t, err)

		// TLS requests will succeed
		client := testClient(backend1)
		re, err := client.Get(backend1.URL)
		assert.Nil(t, err)
		defer re.Body.Close()
		bytes, err := ioutil.ReadAll(re.Body)
		assert.Nil(t, err)
		assert.Equal(t, string(bytes), "backend 1")

		// Close mux, new requests should fail
		mux.Close()
		mux.Wait()

		// use new client to use new connection pool
		client = testClient(backend1)
		re, err = client.Get(backend1.URL)
		if err == nil {
			re.Body.Close()
		}
		assert.NotNil(t, err)
	})

	// TestDisableTLS tests scenario with disabled TLS
	t.Run("DisableTLS", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: true,
			DisableTLS:          true,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		backend1 := &httptest.Server{
			Listener: mux.TLS(),
			Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "backend 1")
			}),
			},
		}
		backend1.StartTLS()
		defer backend1.Close()

		called := false
		sshHandler := sshutils.NewChanHandlerFunc(func(_ context.Context, _ *sshutils.ConnectionContext, nch ssh.NewChannel) {
			called = true
			err := nch.Reject(ssh.Prohibited, "nothing to see here")
			assert.Nil(t, err)
		})

		srv, err := sshutils.NewServer(
			"test",
			utils.NetAddr{AddrNetwork: "tcp", Addr: "localhost:0"},
			sshHandler,
			[]ssh.Signer{signer},
			sshutils.AuthMethods{Password: pass("abc123")},
		)
		assert.Nil(t, err)
		go srv.Serve(mux.SSH())
		defer srv.Close()
		clt, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			Auth:            []ssh.AuthMethod{ssh.Password("abc123")},
			Timeout:         time.Second,
			HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		})
		assert.Nil(t, err)
		defer clt.Close()

		// call new session to initiate opening new channel
		_, err = clt.NewSession()
		assert.NotNil(t, err)
		// make sure the channel handler was called OK
		assert.Equal(t, called, true)

		client := testClient(backend1)
		re, err := client.Get(backend1.URL)
		if err == nil {
			re.Body.Close()
		}
		assert.NotNil(t, err)

		// Close mux, new requests should fail
		mux.Close()
		mux.Wait()
	})

	// NextProto tests multiplexing using NextProto selector
	t.Run("NextProto", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		assert.Nil(t, err)

		mux, err := New(Config{
			Listener:            listener,
			EnableProxyProtocol: true,
		})
		assert.Nil(t, err)
		go mux.Serve()
		defer mux.Close()

		cfg, err := fixtures.LocalTLSConfig()
		assert.Nil(t, err)

		tlsLis, err := NewTLSListener(TLSListenerConfig{
			Listener: tls.NewListener(mux.TLS(), cfg.TLS),
		})
		assert.Nil(t, err)
		go tlsLis.Serve()

		opts := []grpc.ServerOption{
			grpc.Creds(&httplib.TLSCreds{
				Config: cfg.TLS,
			})}
		s := grpc.NewServer(opts...)
		test.RegisterPingerServer(s, &server{})

		errCh := make(chan error, 2)

		go func() {
			errCh <- s.Serve(tlsLis.HTTP2())
		}()

		httpServer := http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "http backend")
			}),
		}
		go func() {
			err := httpServer.Serve(tlsLis.HTTP())
			if err == nil || err == http.ErrServerClosed {
				errCh <- nil
				return
			}
			errCh <- err
		}()

		url := fmt.Sprintf("https://%s", listener.Addr())
		client := cfg.NewClient()
		re, err := client.Get(url)
		assert.Nil(t, err)
		defer re.Body.Close()
		bytes, err := ioutil.ReadAll(re.Body)
		assert.Nil(t, err)
		assert.Equal(t, string(bytes), "http backend")

		creds := credentials.NewClientTLSFromCert(cfg.CertPool, "")

		// Set up a connection to the server.
		conn, err := grpc.Dial(listener.Addr().String(), grpc.WithTransportCredentials(creds), grpc.WithBlock())
		assert.Nil(t, err)
		defer conn.Close()

		gclient := test.NewPingerClient(conn)

		out, err := gclient.Ping(context.TODO(), &test.Request{})
		assert.Nil(t, err)
		assert.Equal(t, out.GetPayload(), "grpc backend")

		// Close mux, new requests should fail
		mux.Close()
		mux.Wait()

		// use new client to use new connection pool
		client = cfg.NewClient()
		re, err = client.Get(url)
		if err == nil {
			re.Body.Close()
		}
		assert.NotNil(t, err)

		httpServer.Close()
		s.Stop()
		// wait for both servers to finish
		for i := 0; i < 2; i++ {
			err := <-errCh
			assert.Nil(t, err)
		}
	})
}

// server is used to implement test.PingerServer
type server struct {
}

func (s *server) Ping(ctx context.Context, req *test.Request) (*test.Response, error) {
	return &test.Response{Payload: "grpc backend"}, nil
}

// clientConfig returns tls client config from test http server
// set up to listen on TLS
func clientConfig(srv *httptest.Server) *tls.Config {
	cert, err := x509.ParseCertificate(srv.TLS.Certificates[0].Certificate[0])
	if err != nil {
		panic(err)
	}

	certpool := x509.NewCertPool()
	certpool.AddCert(cert)
	return &tls.Config{
		RootCAs:    certpool,
		ServerName: fmt.Sprintf("%v", cert.IPAddresses[0].String()),
	}
}

// testClient is a test HTTP client set up for TLS
func testClient(srv *httptest.Server) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: clientConfig(srv),
		},
	}
}

func pass(need string) sshutils.PasswordFunc {
	return func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if string(password) == need {
			return nil, nil
		}
		return nil, fmt.Errorf("passwords don't match")
	}
}
