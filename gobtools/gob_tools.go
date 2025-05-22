// Copyright 2024 Google Inc. All rights reserved.
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
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
)

// To decode an interface type, we need to store the info about the underlying
// concreate type of the value during encoding. When decoding we will use that
// info and the registry map below to recreate a default value of the concrete
// type and then deserialize the stored data into it.
var typeRegistry = make(map[int16]func() CustomDec)

// Enum to represent the underlying type of the interface field.
type interfaceType int16

const (
	nilInterface interfaceType = iota
	nilPointerInterface
	pointerInterface
	valueInterface
)

var typeRegId int16 = 0

// RegisterType registers a concrete type with the type registry, this method should
// be called from an init() function in the package. It incrementally generates an id
// and return to the caller, when encoding an instance of the struct, the id is stored
// along with the actual value of the instance. During decoding the stored id is
// used to look up the function to initiate a default instance of the struct.
func RegisterType(creator func() CustomDec) int16 {
	typeRegId++
	typeRegistry[typeRegId] = creator
	return typeRegId
}

// Interface indicates the struct provides custom encoding logic either thru
// code generation or manual coding.
type CustomEnc interface {
	Encode(buf *bytes.Buffer) error
	GetTypeId() int16
}

// Interface indicates the struct provides custom decoding logic either thru
// code generation or manual coding
type CustomDec interface {
	Decode(buf *bytes.Reader) error
}

// Legacy way to provide custom Gob encoding and decoding logic.
type CustomGob[T any] interface {
	ToGob() *T
	FromGob(data *T)
}

// Encode a string value.
func EncodeString(buf *bytes.Buffer, s string) error {
	b := []byte(s)
	err := binary.Write(buf, binary.BigEndian, int32(len(b)))
	if err != nil {
		return err
	}
	_, err = buf.Write(b)
	return err
}

// Decode a string value.
func DecodeString(buf *bytes.Reader, s *string) error {
	var length int32
	err := binary.Read(buf, binary.BigEndian, &length)
	if err != nil {
		return err
	}
	b := make([]byte, length)
	_, err = io.ReadFull(buf, b)
	if err == nil {
		*s = string(b)
	}

	return err
}

// These two methods can be further optimized using primitive specific encoding
// and decoding methods if it becomes necessary.

// Encode a primitive value.
func EncodeSimple[T any](buf *bytes.Buffer, b T) error {
	return binary.Write(buf, binary.BigEndian, b)
}

// Decode a primitive value.
func DecodeSimple[T any](buf *bytes.Reader, data *T) error {
	return binary.Read(buf, binary.BigEndian, data)
}

// Encode a struct. It uses type assert to leverage Gob to encode the value when
// the struct hasn't be converted to use codegen to generate encoding logic, this
// should be removed once all are converted.
func EncodeStruct(buf *bytes.Buffer, val any) error {
	// val is pointer to either a struct or an interface{}. If it is the latter the
	// type assert below will fail even if the underlying concrete type implements
	// the CustomEnc interface. This is intentional in order for ob to handle the
	// interface case, where it will store the interface info and is albe to properly
	// deserialize it later. Otherwise, it will be serialized as a concrete type,
	// then later it can't be deserialized back to an interface field.
	if encdec, ok := val.(CustomEnc); ok {
		return encdec.Encode(buf)
	} else {
		panic(fmt.Errorf("encoding type is not supported: %T", val))
	}
}

// Encode an interface value.
func EncodeInterface(buf *bytes.Buffer, data any) error {
	if data == nil {
		return EncodeSimple(buf, nilInterface)
	}
	intfType := valueInterface
	if v := reflect.ValueOf(data); v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return fmt.Errorf("nil pointer is not supported in EncodeInterface")
		} else {
			intfType = pointerInterface
		}
	}
	if err := EncodeSimple(buf, intfType); err != nil {
		return err
	}
	val := data.(CustomEnc)
	if err := EncodeSimple(buf, val.GetTypeId()); err != nil {
		return err
	}
	return val.Encode(buf)
}

// Decode a struct. It uses type assert to leverage Gob to decode the value when
// the struct hasn't be converted to use codegen to generate decoding logic, this
// should be removed once all are converted
func DecodeStruct(buf *bytes.Reader, data any) error {
	if encdec, ok := data.(CustomDec); ok {
		return encdec.Decode(buf)
	} else {
		panic(fmt.Errorf("decoding type is not supported: %T", data))
	}
}

// Decode an interface value.
func DecodeInterface(buf *bytes.Reader) (any, error) {
	var intfType interfaceType
	if err := DecodeSimple(buf, &intfType); err != nil || intfType == nilInterface {
		return nil, err
	}
	var typeId int16
	if err := DecodeSimple(buf, &typeId); err != nil {
		return nil, err
	}
	if f, ok := typeRegistry[typeId]; !ok {
		return nil, fmt.Errorf("type not registered: %d", typeId)
	} else {
		val := f()
		if err := val.Decode(buf); err != nil {
			return nil, err
		} else if intfType == valueInterface {
			return reflect.ValueOf(val).Elem().Interface(), nil
		} else {
			return val, nil
		}
	}
}
