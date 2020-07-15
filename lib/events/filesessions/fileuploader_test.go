/*
Copyright 2018 Gravitational, Inc.

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

package filesessions

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/gravitational/teleport/lib/events/test"
	"github.com/gravitational/teleport/lib/utils"

	"github.com/stretchr/testify/assert"
)

// TestStreams tests various streaming upload scenarios
func TestStreams(t *testing.T) {
	utils.InitLoggerForTests(testing.Verbose())

	dir, err := ioutil.TempDir("", "teleport-streams")
	assert.Nil(t, err)
	defer os.RemoveAll(dir)

	handler, err := NewHandler(Config{
		Directory: dir,
	})
	assert.Nil(t, err)

	t.Run("Stream", func(t *testing.T) {
		test.Stream(t, handler)
	})
	t.Run("UploadDownload", func(t *testing.T) {
		test.UploadDownload(t, handler)
	})
	t.Run("DownloadNotFound", func(t *testing.T) {
		test.DownloadNotFound(t, handler)
	})
}
