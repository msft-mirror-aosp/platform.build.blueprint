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
	"bufio"
	"bytes"
	"reflect"
	"slices"
	"testing"
)

func TestEncodeDecodeGlobCache(t *testing.T) {
	globs := []GlobResult{
		{
			Pattern: "*/*.go",
			Deps:    []string{"a", "b"},
			Matches: []string{"a/a.go", "b/b.go"},
		},
		{
			Pattern:  "*/*.c",
			Excludes: []string{"*/a.c", "*/b.c"},
			Deps:     []string{"c", "d"},
			Matches:  []string{"c/c.c", "d/d.c"},
		},
	}

	buf := &bytes.Buffer{}
	w := bufio.NewWriter(buf)
	encoder, err := newGlobFileEncoder(w)
	if err != nil {
		t.Fatalf("failed to create encoder: %s", err)
	}
	err = encoder.encode(globs)
	if err != nil {
		t.Fatalf("failed to encode globs: %s", err)
	}
	err = w.Flush()
	if err != nil {
		t.Fatalf("failed to flush writer: %s", err)
	}

	r := bufio.NewReader(buf)
	decoder, err := newGlobFileDecoder(r)
	if err != nil {
		t.Fatalf("failed to create decoder: %s", err)
	}
	i, errPromise := decoder.iter()
	decodedGlobs := slices.Collect(i)
	if err := errPromise(); err != nil {
		t.Fatalf("failed to decode globs: %s", err)
	}

	if !reflect.DeepEqual(globs, decodedGlobs) {
		t.Errorf("expected globs %#v, got %#v", globs, decodedGlobs)
	}
}
