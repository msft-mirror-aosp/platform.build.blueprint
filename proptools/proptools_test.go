// Copyright 2020 Google Inc. All rights reserved.
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
)

func TestPropertyNameForField(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "short",
			input: "S",
			want:  "s",
		},
		{
			name:  "long",
			input: "String",
			want:  "string",
		},
		{
			name:  "uppercase",
			input: "STRING",
			want:  "STRING",
		},
		{
			name:  "mixed",
			input: "StRiNg",
			want:  "stRiNg",
		},
		{
			name:  "underscore",
			input: "Under_score",
			want:  "under_score",
		},
		{
			name:  "uppercase underscore",
			input: "UNDER_SCORE",
			want:  "UNDER_SCORE",
		},
		{
			name:  "x86",
			input: "X86",
			want:  "x86",
		},
		{
			name:  "x86_64",
			input: "X86_64",
			want:  "x86_64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PropertyNameForField(tt.input); got != tt.want {
				t.Errorf("PropertyNameForField(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFieldNameForProperty(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "short lowercase",
			input: "s",
			want:  "S",
		},
		{
			name:  "short uppercase",
			input: "S",
			want:  "S",
		},
		{
			name:  "long lowercase",
			input: "string",
			want:  "String",
		},
		{
			name:  "long uppercase",
			input: "STRING",
			want:  "STRING",
		},
		{
			name:  "mixed",
			input: "StRiNg",
			want:  "StRiNg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FieldNameForProperty(tt.input); got != tt.want {
				t.Errorf("FieldNameForProperty(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestClearField(t *testing.T) {
	props := struct {
		i  int
		s  string
		ps *string
		ss []string
		c  struct {
			n int
		}
	}{}

	props.i = 42
	Clear(&props.i)
	if props.i != 0 {
		t.Error("int field is not cleared to zero.")
	}

	props.s = "foo"
	Clear(&props.s)
	if props.s != "" {
		t.Error("string field is not cleared to zero.")
	}

	props.ps = StringPtr("foo")
	Clear(&props.ps)
	if props.ps != nil {
		t.Error("string pointer field is not cleared to zero.")
	}

	props.ss = []string{"foo"}
	Clear(&props.ss)
	if props.ss != nil {
		t.Error("string array field is not cleared to zero.")
	}

	props.c.n = 42
	Clear(&props.c)
	if props.c.n != 0 {
		t.Error("struct field is not cleared to zero.")
	}
}

func TestIsConfigurable(t *testing.T) {
	testCases := []struct {
		name     string
		value    interface{}
		expected bool
	}{
		{
			name:     "Configurable string",
			value:    Configurable[string]{},
			expected: true,
		},
		{
			name:     "Configurable string list",
			value:    Configurable[[]string]{},
			expected: true,
		},
		{
			name:     "Configurable bool",
			value:    Configurable[bool]{},
			expected: true,
		},
		{
			name: "Other struct with a bool as the first field",
			value: struct {
				x bool
			}{},
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			value := reflect.ValueOf(tc.value)
			if isConfigurable(value.Type()) != tc.expected {
				t.Errorf("Expected isConfigurable to return %t", tc.expected)
			}
		})
	}
}
