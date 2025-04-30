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

package main

import "github.com/google/blueprint/uniquelist"

//go:generate go run gob_gen.go -source gob_test_data.go

type TestEchoInterface interface {
	EchoTest(string) string
}

// @auto-generate: gob
type TestStruct struct {
	TestEcho
	f1  string
	f2  int16
	f3  int32
	f4  bool
	f5  int64
	f6  string
	f7  TestEcho
	f8  uint16
	f9  uint32
	f10 uint64
	f11 []string
	f12 map[string]int
	f13 *string
	f14 int
	f15 []int
	f16 []TestEcho
	f17 *TestEcho
	f18 TestEchoInterface
	f19 testStrings
	f20 uniquelist.UniqueList[TestEcho]
	f21 uniquelist.UniqueList[TestEchoInterface]
}

type testStrings []string

// @auto-generate: gob
type TestEcho struct {
	EchoStr string
}

func (t TestEcho) EchoTest(string) string {
	return t.EchoStr
}
