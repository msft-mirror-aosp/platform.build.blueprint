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

package syncmap

import "testing"

func TestSyncmapLoadOrCompute(t *testing.T) {
	timesCalled := 0
	computer := func() int {
		timesCalled++
		if timesCalled > 1 {
			panic("Called multiple times")
		}
		return 5 + 5
	}
	m := SyncMap[string, int]{}
	if v, loaded := m.LoadOrCompute("ten", computer); v != 10 || loaded {
		t.Fatalf("Expected loaded=false and v=10, got loaded=%t and v=%d\n", loaded, v)
	}
	if v, loaded := m.LoadOrCompute("ten", computer); v != 10 || !loaded {
		t.Fatalf("Expected loaded=true and v=10, got loaded=%t and v=%d\n", loaded, v)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected a panic from being called multiple times")
		}
	}()
	m.LoadOrCompute("10", computer)
}

func TestSyncmapLoadOrComputeWithPanic(t *testing.T) {
	m := SyncMap[string, int]{}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("Expected a panic")
			}
		}()
		m.LoadOrCompute("mykey", func() int {
			panic("Oh no!")
		})
	}()
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("Expected a second panic")
			}
		}()
		m.Load("mykey")
	}()

	// Expect no panic
	m.Load("otherkey")
}
