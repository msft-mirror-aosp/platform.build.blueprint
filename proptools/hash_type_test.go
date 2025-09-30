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

package proptools

import (
	"reflect"
	"testing"

	type_test1 "github.com/google/blueprint/proptools/type_test1/type_test"
	type_test2 "github.com/google/blueprint/proptools/type_test2/type_test"
)

func TestTypeHashSuccessful(t *testing.T) {
	for _, testCase := range hashTestCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := typeHashSlow(reflect.TypeOf(testCase.data))
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
		})
	}
}

func TestTypeHashOfDifferentTypesIsDifferent(t *testing.T) {
	type t1 struct {
		s string
	}
	type t2 struct {
		s string
	}

	h1, err := typeHashSlow(reflect.TypeOf(t1{}))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := typeHashSlow(reflect.TypeOf(t2{}))
	if err != nil {
		t.Fatal(err)
	}

	if h1 == h2 {
		t.Errorf("expected hashes of %#v and %#v to be different, got %v and %v", t1{}, t2{}, h1, h2)
	}
}

func TestTypeHashOfDifferentRuntimeTypes(t *testing.T) {
	t1 := reflect.StructOf([]reflect.StructField{
		{
			Name: "S",
			Type: reflect.TypeOf(""),
		},
	})
	t2 := reflect.StructOf([]reflect.StructField{
		{
			Name: "S",
			Type: reflect.TypeOf(""),
		},
	})
	t3 := reflect.StructOf([]reflect.StructField{
		{
			Name: "S2",
			Type: reflect.TypeOf(""),
		},
	})

	h1, err := typeHashSlow(t1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := typeHashSlow(t2)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := typeHashSlow(t3)
	if err != nil {
		t.Fatal(err)
	}

	// Identical runtime defined types are indistinguishable.
	if h1 != h2 {
		t.Errorf("expected hashes of %s and %s to be same, got %v and %v", t1, t2, h1, h2)
	}

	// Runtime defined types with the same contents but different field names give different hashes.
	if h1 == h3 {
		t.Errorf("expected hashes of %s and %s to be different, got %v and %v", t1, t3, h1, h3)
	}
}

func TestTypeHashOfDifferentTypesWithSamePackageShortName(t *testing.T) {
	s1 := type_test1.TypeTest{}
	s2 := type_test2.TypeTest{}

	h1, err := typeHashSlow(reflect.TypeOf(s1))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := typeHashSlow(reflect.TypeOf(s2))
	if err != nil {
		t.Fatal(err)
	}

	if h1 == h2 {
		t.Errorf("expected hashes of %#v and %#v to be different, got %v and %v", s1, s2, h1, h2)
	}
}
