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

package gobtools

import (
	"bytes"
	"reflect"
	"testing"
)

type testStruct struct {
	name string
}

func (r testStruct) Encode(buf *bytes.Buffer) error {
	var err error

	if err = EncodeString(buf, r.name); err != nil {
		return err
	}
	return err
}

func (r *testStruct) Decode(buf *bytes.Reader) error {
	var err error

	err = DecodeString(buf, &r.name)
	if err != nil {
		return err
	}

	return err
}

func (r testStruct) GetTypeId() int16 {
	return -1
}

func TestEncDecReferencesStruct(t *testing.T) {
	defTestStruct := &testStruct{name: "string value for test"}
	testCases := []struct {
		name     string
		encoded  *testStruct
		decoded1 *testStruct
		decoded2 *testStruct
	}{
		{
			name:     "struct reference",
			encoded:  defTestStruct,
			decoded1: &testStruct{},
			decoded2: &testStruct{},
		},
	}

	for _, tc := range testCases {
		var err error
		buf := new(bytes.Buffer)
		ctx := NewReferencesEncoderForTest()
		if err = EncodeReference(ctx, tc.encoded, buf, func(value *testStruct, buf *bytes.Buffer) error {
			return value.Encode(buf)
		}); err != nil {
			t.Errorf("failed to encode reference: %v", err)
		}
		if err = EncodeReference(ctx, tc.encoded, buf, func(value *testStruct, buf *bytes.Buffer) error {
			return value.Encode(buf)
		}); err != nil {
			t.Errorf("failed to encode reference: %v", err)
		}
		if err = ctx.EncodeReferences(); err != nil {
			t.Errorf("failed to encode references: %v", err)
		}
		reader := bytes.NewReader(buf.Bytes())
		if tc.decoded1, err = DecodeReference(ctx, tc.decoded1, reader, func(value *testStruct, buf *bytes.Reader) error {
			return value.Decode(buf)
		}); err != nil {
			t.Errorf("failed to decode references: %v", err)
		}
		if tc.decoded2, err = DecodeReference(ctx, tc.decoded2, reader, func(value *testStruct, buf *bytes.Reader) error {
			return value.Decode(buf)
		}); err != nil {
			t.Errorf("failed to decode references: %v", err)
		}
		if !reflect.DeepEqual(tc.encoded, tc.decoded1) {
			t.Errorf("the decoded data is different from the origin: expected:\n  %#v\n got:\n  %#v", tc.encoded, tc.decoded1)
		}
		if !reflect.DeepEqual(tc.encoded, tc.decoded2) {
			t.Errorf("the decoded data is different from the origin: expected:\n  %#v\n got:\n  %#v", tc.encoded, tc.decoded2)
		}
		if tc.decoded1 != tc.decoded2 {
			t.Errorf("should decode to the same reference: \n  %#v\n %#v", tc.decoded1, tc.decoded2)
		}
	}
}

func TestEncDecReferencesString(t *testing.T) {
	defString := "string value for test"
	var decode1 string
	var decode2 string
	testCases := []struct {
		name     string
		encoded  *string
		decoded1 *string
		decoded2 *string
	}{
		{
			name:     "string reference",
			encoded:  &defString,
			decoded1: &decode1,
			decoded2: &decode2,
		},
	}

	for _, tc := range testCases {
		var err error
		buf := new(bytes.Buffer)
		ctx := NewReferencesEncoderForTest()
		if err = EncodeReference(ctx, tc.encoded, buf, func(value *string, buf *bytes.Buffer) error {
			return EncodeString(buf, *value)
		}); err != nil {
			t.Errorf("failed to encode reference: %v", err)
		}
		if err = EncodeReference(ctx, tc.encoded, buf, func(value *string, buf *bytes.Buffer) error {
			return EncodeString(buf, *value)
		}); err != nil {
			t.Errorf("failed to encode reference: %v", err)
		}
		if err = ctx.EncodeReferences(); err != nil {
			t.Errorf("failed to encode references: %v", err)
		}
		reader := bytes.NewReader(buf.Bytes())
		if tc.decoded1, err = DecodeReference(ctx, tc.decoded1, reader, func(value *string, buf *bytes.Reader) error {
			return DecodeString(buf, value)
		}); err != nil {
			t.Errorf("failed to decode references: %v", err)
		}
		if tc.decoded2, err = DecodeReference(ctx, tc.decoded2, reader, func(value *string, buf *bytes.Reader) error {
			return DecodeString(buf, value)
		}); err != nil {
			t.Errorf("failed to decode references: %v", err)
		}
		if !reflect.DeepEqual(tc.encoded, tc.decoded1) {
			t.Errorf("the decoded data is different from the origin: expected:\n  %#v\n got:\n  %#v", tc.encoded, tc.decoded1)
		}
		if !reflect.DeepEqual(tc.encoded, tc.decoded2) {
			t.Errorf("the decoded data is different from the origin: expected:\n  %#v\n got:\n  %#v", tc.encoded, tc.decoded2)
		}
		if tc.decoded1 != tc.decoded2 {
			t.Errorf("should decode to the same reference: \n  %#v\n %#v", tc.decoded1, tc.decoded2)
		}
	}
}
