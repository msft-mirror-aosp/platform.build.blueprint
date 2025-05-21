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

import (
	"reflect"
	"testing"

	"github.com/google/blueprint/depset"
	"github.com/google/blueprint/gobtools"
	"github.com/google/blueprint/gobtools/test"
	"github.com/google/blueprint/uniquelist"
)

func TestPathGobEncDec(t *testing.T) {
	strValue := "string value for test"
	defaultEcho := TestEcho{"111111111"}
	transString := depset.New(depset.PREORDER, []string{"111111111"}, nil)
	depsetString := depset.New(depset.PREORDER, []string{"222222222"}, []depset.DepSet[string]{transString})
	transTestEcho := depset.New(depset.PREORDER, []TestEcho{defaultEcho}, nil)
	depsetTestEcho := depset.New(depset.PREORDER, []TestEcho{defaultEcho}, []depset.DepSet[TestEcho]{transTestEcho})
	transTestEchoInterface := depset.New(depset.PREORDER, []TestEchoInterface{defaultEcho}, nil)
	depsetTestEchoInterface := depset.New(depset.PREORDER, []TestEchoInterface{defaultEcho}, []depset.DepSet[TestEchoInterface]{transTestEchoInterface})
	testCases := []struct {
		name    string
		origin  gobtools.CustomEnc
		decoded gobtools.CustomDec
	}{
		{
			name:    "TestStruct with default values",
			origin:  &TestStruct{},
			decoded: &TestStruct{},
		},
		{
			name: "TestStruct with value interface",
			origin: &TestStruct{
				f18: TestEcho{"aaaa"},
				f21: uniquelist.Make([]TestEchoInterface{defaultEcho}),
				f24: test.TypeStruct{Name: "fffffffff"},
			},
			decoded: &TestStruct{},
		},
		{
			name: "TestStruct",
			origin: &TestStruct{
				TestEcho: TestEcho{"222222222"},
				f1:       "333333333",
				f2:       111,
				f3:       222,
				f4:       true,
				f5:       333,
				f6:       "444444444",
				f7:       TestEcho{"444444444"},
				f8:       444,
				f9:       555,
				f10:      666,
				f11:      []string{"a", "bb", "ccc"},
				f12: map[string]int{
					"f12a": 1,
					"f12b": 2,
					"f12c": 3,
				},
				f13: &strValue,
				f14: 777,
				f15: []int{1, 2, 3},
				f16: []TestEcho{
					{"555555555"},
					{"666666666"},
				},
				f17: &defaultEcho,
				f18: &TestEcho{"aaaa"},
				f19: testStrings{"bbbb", "cccc", "dddd"},
				f20: uniquelist.Make([]TestEcho{defaultEcho}),
				f21: uniquelist.Make([]TestEchoInterface{&defaultEcho}),
				f22: test.TypeStruct{Name: "aaaaaaaaa"},
				f23: []test.TypeAlias{
					{
						{Name: "bbbbbbbbb"},
						{Name: "ccccccccc"},
					},
					{
						{Name: "ddddddddd"},
						{Name: "eeeeeeeee"},
					},
				},
				f24: &test.TypeStruct{Name: "fffffffff"},
				f25: test.TypeIdent{Name: "ggggggggg"},
				f26: depsetTestEcho,
				f27: depsetTestEchoInterface,
				f28: map[int][]string{
					1: {"aaaaaaaaa", "bbbbbbbbb"},
					2: {"ccccccccc", "ddddddddd"},
				},
				f29: [][]string{
					{"aaaaaaaaa", "bbbbbbbbb"},
					{"ccccccccc", "ddddddddd"},
				},
				f30: depsetString,
				f31: &defaultEcho,
			},
			decoded: &TestStruct{},
		},
		{
			name:    "testEchos",
			origin:  &testEchos{defaultEcho},
			decoded: &testEchos{},
		},
		{
			name: "testStringMap",
			origin: &testStringMap{
				"111111111": []string{"222222222", "333333333"},
				"222222222": []string{"444444444", "555555555"},
			},
			decoded: &testStringMap{},
		},
		{
			name: "testEchoMap",
			origin: &testEchoMap{
				defaultEcho: &TestEcho{"aaaaaaaaa"},
			},
			decoded: &testEchoMap{},
		},
	}

	for _, tc := range testCases {
		data, err := tc.origin.GobEncode()
		if err != nil {
			t.Errorf("failed to encode %s: %v", tc.name, err)
		}
		if err := tc.decoded.GobDecode(data); err != nil {
			t.Errorf("failed to decode %s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(tc.origin, tc.decoded) {
			t.Errorf("the decoded data is different from the origin: expected:\n  %#v\n got:\n  %#v", tc.origin, tc.decoded)
		}
	}
}
