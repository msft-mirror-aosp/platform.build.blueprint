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
	"encoding/gob"
	"fmt"
	"io"
)

type CustomEnc interface {
	GobEncode() ([]byte, error)
}

type CustomDec interface {
	GobDecode(b []byte) error
	Decode(buf *bytes.Reader) error
}

type CustomGob[T any] interface {
	ToGob() *T
	FromGob(data *T)
}

func CustomGobEncode[T any](cg CustomGob[T]) ([]byte, error) {
	w := new(bytes.Buffer)
	encoder := gob.NewEncoder(w)
	err := encoder.Encode(cg.ToGob())
	if err != nil {
		return nil, err
	}

	return w.Bytes(), nil
}

func CustomGobDecode[T any](data []byte, cg CustomGob[T]) error {
	r := bytes.NewBuffer(data)
	var value T
	decoder := gob.NewDecoder(r)
	err := decoder.Decode(&value)
	if err != nil {
		return err
	}
	cg.FromGob(&value)

	return nil
}

func EncodeString(buf *bytes.Buffer, s string) error {
	b := []byte(s)
	err := binary.Write(buf, binary.BigEndian, int32(len(b)))
	if err != nil {
		return err
	}
	_, err = buf.Write(b)
	return err
}

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
func EncodeSimple[T any](buf *bytes.Buffer, b T) error {
	return binary.Write(buf, binary.BigEndian, b)
}

func DecodeSimple[T any](buf *bytes.Reader, data *T) error {
	return binary.Read(buf, binary.BigEndian, data)
}

func EncodeStruct(buf *bytes.Buffer, val any) error {
	var err error
	if encdec, ok := val.(CustomEnc); ok && false {
		var data []byte
		data, err = encdec.GobEncode()
		if err != nil {
			return err
		}
		_, err = buf.Write(data)
		return err
	} else {
		encoder := gob.NewEncoder(buf)
		return encoder.Encode(val)
	}
}

func DecodeStruct(buf *bytes.Reader, data any) error {
	if encdec, ok := data.(CustomDec); ok && false {
		return encdec.Decode(buf)
	} else {
		fmt.Printf("BBB: %T\n", data)
		decoder := gob.NewDecoder(buf)
		return decoder.Decode(data)
	}
}
