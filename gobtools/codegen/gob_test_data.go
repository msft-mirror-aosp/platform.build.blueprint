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
	"bytes"
	"cmp"
	"unique"

	"github.com/google/blueprint/depset"
	"github.com/google/blueprint/gobtools"
	"github.com/google/blueprint/gobtools/test"
	"github.com/google/blueprint/proptools"
	"github.com/google/blueprint/uniquelist"
)

//go:generate go run ../codegen

type TestEchoInterface interface {
	EchoTest(string) string
}

// @auto-generate: gob
type TestStruct struct {
	TestEcho
	f1       string
	f2       int16
	f3       int32
	f4       bool
	f5       int64
	f6       string
	f7       TestEcho
	f8       uint16
	f9       uint32
	f10      uint64
	f11      []string
	f12      map[string]int
	f13      *string
	f14      int
	f15      []int
	f16      []TestEcho
	f17      *TestEcho
	f18      TestEchoInterface
	f19      testStrings
	f20      uniquelist.UniqueList[TestEcho]
	f21      uniquelist.UniqueList[TestEchoInterface]
	f22      test.TypeStruct
	f23      []test.TypeAlias
	f24      test.TypeInterface
	f25      test.TypeIdent
	f26      depset.DepSet[TestEcho]
	f27      depset.DepSet[TestEchoInterface]
	f28      map[int][]string
	f29      [][]string
	f30      depset.DepSet[string]
	f31      any
	f32      []*test.TypeStruct
	f33, f34 string
	f35      struct{ s string }
	f36      TestGeneric[int32]
	*TestEmbedPtr
	f38 test.TypeBasic
	f39 [2]uint64
	f40 map[string]map[int][]*bool
	f41 unique.Handle[TestEcho]
	f42 testEchoHandle
}

type testEchoHandle = unique.Handle[TestEcho]
type testStrings []string

// @auto-generate: gob
type TestEcho struct {
	EchoStr string
}

func (t TestEcho) Compare(other TestEcho) int {
	return cmp.Compare(t.EchoStr, other.EchoStr)
}

func (t TestEcho) EchoTest(string) string {
	return t.EchoStr
}

type TestGeneric[T any] struct {
	t T
}

func (t *TestGeneric[T]) Decode(ctx gobtools.EncContext, buf *bytes.Reader) error {
	return gobtools.DecodeSimple(buf, &t.t)
}

func (t *TestGeneric[T]) Encode(ctx gobtools.EncContext, buf *bytes.Buffer) error {
	return gobtools.EncodeSimple(buf, t.t)
}

func (t *TestGeneric[T]) GetTypeId() int16 {
	return -1
}

func (t *TestGeneric[T]) Hash(hasher *proptools.Hasher) error {
	return nil
}

// @auto-generate: gob
type testEchos []TestEchoInterface

// @auto-generate: gob
type testStringMap map[string][]string

// @auto-generate: gob
type testEchoMap map[TestEcho]*TestEcho

// @auto-generate: gob
type TestEmbedPtr struct {
	f37 string
}

// @auto-generate: gob
type TestPtrs struct {
	f1 *TestEcho
	f2 *TestEcho
}

// @auto-generate: gob
type HashStruct struct {
	I   int
	S   string
	B   bool
	F64 int64
}

// @auto-generate: gob
type Pointers struct {
	A *int
	S *string
}

// @auto-generate: gob
type Slices struct {
	A []int
	B []string
}

// @auto-generate: gob
type Maps struct {
	M map[string]int
}

// @auto-generate: gob
type Inner struct {
	Val string
}

// @auto-generate: gob
type Embedded struct {
	Inner // Embedded field
	F     int32
}

// @auto-generate: gob
type Nested struct {
	A Inner
	B *Inner
}

type MyInterface interface {
	GetValue() string
}

// @auto-generate: gob
type InterfaceImpl1 struct{ V string }

func (i InterfaceImpl1) GetValue() string { return i.V }

// @auto-generate: gob
type InterfaceImpl2 struct{ V string }

func (i InterfaceImpl2) GetValue() string { return i.V }

// @auto-generate: gob
type InterfaceHolder struct {
	I MyInterface
}

// @auto-generate: gob
type Unexported struct {
	Exported   string
	unexported string // This tests if your hash handles unexported fields
}

// @auto-generate: gob
type Complex struct {
	MapOfSlices    map[string][]int
	SliceOfPtrs    []*Inner
	InterfaceField MyInterface
}

// @auto-generate: gob
type PointerDuplication struct {
	F1 *string
	F2 *string
}

// @auto-generate: gob
type Type1 struct{ S string }

// @auto-generate: gob
type Type2 struct{ S string }

// @auto-generate: gob
type InterfaceWrapper struct {
	V any
}
