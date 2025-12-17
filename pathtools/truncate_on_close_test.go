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
	"os"
	"testing"
)

func TestOpenWithTruncateOnCloseTruncate(t *testing.T) {
	type testCase struct {
		name     string
		content1 string
		content2 string
	}

	testCases := []testCase{
		{"grow", "abc", "abcde"},
		{"shrink", "abc", "ab"},
		{"same", "abc", "def"},
		{"zero", "abc", ""},
		{"from zero", "", "abc"},
	}

	run := func(t *testing.T, fs FileSystem, test testCase) {
		path := "TestOpenWithTruncateOnClose_" + test.name
		writeContent := func(content string) {
			t.Helper()
			f, err := OpenWithTruncateOnClose(fs, path)
			if err != nil {
				t.Fatalf("OpenWithTruncateOnClose: %v", err)
			}
			_, err = f.Write([]byte(content))
			if err != nil {
				f.Close()
				t.Fatalf("Write: %v", err)
			}
			err = f.Close()
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
		}

		writeContent(test.content1)
		writeContent(test.content2)

		got, err := readFile(fs, path)
		if err != nil {
			t.Fatalf("readFile: %v", err)
		}

		if got != test.content2 {
			t.Errorf("expected %q got %q", test.content2, got)
		}
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			t.Run("mock", func(t *testing.T) {
				run(t, MockFs(nil), test)
			})
			t.Run("os", func(t *testing.T) {
				run(t, NewOsFs(os.TempDir()), test)
			})
		})
	}
}
