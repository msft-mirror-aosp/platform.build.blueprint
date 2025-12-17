// Copyright 2025 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pathtools

import (
	"errors"
	"io"
	"os"
)

const OutFilePermissions = 0666

// OpenWithTruncateOnClose opens a file for writing, but does not use O_TRUNC so that the existing filesystem
// blocks are not deallocated.  The file will be truncated to the number of bytes written on close.
func OpenWithTruncateOnClose(fs FileSystem, path string) (io.WriteCloser, error) {
	f, err := fs.OpenFile(path, os.O_WRONLY|os.O_CREATE, OutFilePermissions)
	if err != nil {
		return nil, err
	}
	return &truncateOnCloseWrapper{
		w: f,
	}, nil
}

type truncateOnCloseWrapper struct {
	w     WriteTruncateCloser
	count int
}

func (t *truncateOnCloseWrapper) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	t.count += n
	return n, err
}

func (t *truncateOnCloseWrapper) Close() error {
	err := t.w.Truncate(int64(t.count))
	err2 := t.w.Close()
	return errors.Join(err, err2)
}
