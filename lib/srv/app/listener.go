/*
Copyright 2020 Gravitational, Inc.

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

package app

import (
	"context"
	"net"

	"golang.org/x/crypto/ssh"

	"github.com/gravitational/teleport/lib/utils"

	"github.com/gravitational/trace"
)

type Listener struct {
	connCh    chan *utils.ChConn
	localAddr net.Addr

	closeContext context.Context
	closeFunc    context.CancelFunc
}

func NewListener(ctx context.Context, sconn ssh.Conn, channel ssh.Channel) *Listener {
	closeContext, closeFunc := context.WithCancel(ctx)

	conn := utils.NewChConn(sconn, channel)
	connCh := make(chan *utils.ChConn, 1)
	connCh <- conn

	return &Listener{
		connCh:       connCh,
		localAddr:    conn.LocalAddr(),
		closeContext: closeContext,
		closeFunc:    closeFunc,
	}
}

func (l *Listener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connCh:
		return conn, nil
	case <-l.closeContext.Done():
		return nil, trace.BadParameter("closing context")
	}
}

// TODO: This needs to be filled out.
func (l *Listener) Close() error {
	l.closeFunc()
	return nil
}

func (l *Listener) Addr() net.Addr {
	return l.localAddr
}