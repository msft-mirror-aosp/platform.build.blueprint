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
	"unsafe"

	"github.com/google/blueprint/dbtools"
	"github.com/google/blueprint/proptools"
	"github.com/google/blueprint/syncmap"
)

type EncContext interface {
	ReferenceEnc
}

type ReferenceEnc interface {
	EncodeReferences() error
	EncodeReference(value any, buf *bytes.Buffer, typ string, encode func(value any, buf *bytes.Buffer) error) error
	DecodeReference(buf *bytes.Reader, decode func(buf *bytes.Reader) (any, error)) (any, error)
}

func NewEncContext(db dbtools.KeyValueStore) EncContext {
	return NewReferencesEncoder(db)
}

type ReferencesEncoder struct {
	encodedReferences syncmap.SyncMap[any, *encodedReference]
	decodedReferences syncmap.SyncMap[proptools.Hash, any]
	db                dbtools.KeyValueStore
}

// NewReferencesEncoder creates and initializes a new ReferencesEncoder.
func NewReferencesEncoder(db dbtools.KeyValueStore) *ReferencesEncoder {
	ctx := &ReferencesEncoder{
		db: db,
	}
	return ctx
}

func NewReferencesEncoderForTest() *ReferencesEncoder {
	ctx := &ReferencesEncoder{
		db: &dbtools.InMemKeyValueStore{},
	}
	return ctx
}

// encodedReference stores information about an encoded value reference.
type encodedReference struct {
	valueRefId          proptools.Hash // The unique hash ID for the value.
	valueEncodingBuffer *bytes.Buffer  // The buffer containing the encoded actual value (including its own ref ID and length).
}

func (b *ReferencesEncoder) openForTests() error {
	if b.db != nil {
		panic(fmt.Errorf("db is already open"))
	}

	b.db = &dbtools.InMemKeyValueStore{}
	return nil
}

func (c *ReferencesEncoder) EncodeReferences() error {
	var err error
	c.encodedReferences.Range(func(_ any, value *encodedReference) bool {
		if err = c.db.Put(hashToBytes(value.valueRefId), value.valueEncodingBuffer.Bytes()); err != nil {
			return false
		}
		return true
	})

	return err
}

func (c *ReferencesEncoder) EncodeReference(value any, buf *bytes.Buffer, typ string, encode func(value any, buf *bytes.Buffer) error) error {
	var encStruct *encodedReference
	var ok bool

	// Check if the value has already been encoded.
	if encStruct, ok = c.encodedReferences.Load(value); !ok {
		// If the value is encountered for the first time:
		// Calculate a unique hash for the value using the type's specific hash seed.
		ref, err := proptools.CalculateHashReflection(valueHashConfig{
			typ:   typ,
			value: value,
		})
		if err != nil {
			return err
		}

		// Encode the calculated reference ID into the main buffer (where the value's full encoded data will reside).
		if err := EncodeUint64(buf, ref[0]); err != nil {
			return err
		}

		// Create a new encodedReference struct to store the value's details.
		encStruct = &encodedReference{
			valueRefId:          ref,
			valueEncodingBuffer: new(bytes.Buffer), // Buffer to hold the actual value's encoded data.
		}

		if err := encode(value, encStruct.valueEncodingBuffer); err != nil {
			return err
		}

		// Store the newly created encodedReference in the type-specific map.
		c.encodedReferences.LoadOrStore(value, encStruct)
		return nil // Successfully encoded and stored the new value reference.
	}
	// If the value has been encoded before, just encode its existing reference ID
	// into the output buffer. This optimizes for repeated values.
	return EncodeUint64(buf, encStruct.valueRefId[0])
}

func (c *ReferencesEncoder) DecodeReference(buf *bytes.Reader, decode func(buf *bytes.Reader) (any, error)) (any, error) {
	var refVal uint64 // Variable to store the decoded reference ID.

	// Decode the reference ID of the value from the input stream.
	if err := DecodeUint64(buf, &refVal); err != nil {
		return nil, err // Return error if decoding the reference fails.
	}

	ref := proptools.Hash{refVal}

	// Try to load the value using its reference ID from the decoded values cache.
	if v, ok := c.decodedReferences.Load(ref); !ok {
		data, err := c.db.Get(hashToBytes(ref))
		if err != nil {
			panic(fmt.Errorf("failed to Get from db: %v", err))
			return nil, err
		}

		// Decode the actual value from its raw byte slice using the provided 'decode' function.
		var value any
		if value, err = decode(bytes.NewReader(data)); err != nil {
			return nil, err // Return error if decoding the value from raw bytes fails.
		}
		// Store the newly decoded value in the cache for future lookups.
		c.decodedReferences.LoadOrStore(ref, value)
		return value, nil // Return the decoded value.
	} else {
		// If the value is found in the cache, return the cached value.
		return v, nil // Assert type and return.
	}
}

type valueHashConfig struct {
	typ   string
	value any
}

func hashToBytes(value proptools.Hash) []byte {
	var ret [proptools.HashSize]byte
	value.PutBigEndian(ret[:])
	return ret[:]
}

func EncodeReference[T comparable](c ReferenceEnc, value T, buf *bytes.Buffer, encode func(v T, buf *bytes.Buffer) error) error {
	var defValue T
	typ := reflect.TypeOf(defValue)
	return c.EncodeReference(value, buf, typ.String(), func(v any, buf *bytes.Buffer) error {
		return encode(v.(T), buf)
	})
}

// DecodeReference handles the decoding of a value by reference.
// It reads a reference ID from the buffer, then looks up the corresponding
// value. If not already decoded, it decodes from raw data and caches the result.
func DecodeReference[T comparable](c ReferenceEnc, value T, buf *bytes.Reader, decode func(v T, buf *bytes.Reader) error) (T, error) {
	v, _ := c.DecodeReference(buf, func(buf *bytes.Reader) (any, error) {
		if err := decode(value, buf); err != nil {
			return nil, err
		}
		return value, nil
	})
	return v.(T), nil // Assert type and return.
}

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
	Encode(ctx EncContext, buf *bytes.Buffer) error
	GetTypeId() int16
}

// Interface indicates the struct provides custom decoding logic either thru
// code generation or manual coding
type CustomDec interface {
	Decode(ctx EncContext, buf *bytes.Reader) error
}

// Encode a string value.
func EncodeString(buf *bytes.Buffer, s string) error {
	err := EncodeInt(buf, len(s))
	if err != nil {
		return err
	}
	_, err = buf.WriteString(s)
	return err
}

// Decode a string value.
func DecodeString(buf *bytes.Reader, s *string) error {
	var length int
	err := DecodeInt(buf, &length)
	if err != nil {
		return err
	}
	b := make([]byte, length)
	_, err = io.ReadFull(buf, b)
	if err == nil {
		*s = unsafe.String(unsafe.SliceData(b), len(b))
	}

	return err
}

// Encode a primitive value.
func EncodeSimple[T any](buf *bytes.Buffer, b T) error {
	return binary.Write(buf, binary.BigEndian, b)
}

// Decode a primitive value.
func DecodeSimple[T any](buf *bytes.Reader, data *T) error {
	return binary.Read(buf, binary.BigEndian, data)
}

func EncodeBool(buf *bytes.Buffer, b bool) error {
	var c byte = 0
	if b {
		c = 1
	}
	_, err := buf.Write([]byte{c})
	return err
}

func DecodeBool(buf *bytes.Reader, b *bool) error {
	c, err := buf.ReadByte()
	if err != nil {
		return err
	}
	*b = c != 0
	return nil
}

func EncodeVarint[T int | int16 | int32 | int64](buf *bytes.Buffer, i T) error {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutVarint(b[:], int64(i))
	_, err := buf.Write(b[:n])
	return err
}

func EncodeUvarint[T uint | uint16 | uint32 | uint64](buf *bytes.Buffer, i T) error {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], uint64(i))
	_, err := buf.Write(b[:n])
	return err
}

func DecodeVarint[T int | int16 | int32 | int64](buf *bytes.Reader, i *T) error {
	n, err := binary.ReadVarint(buf)
	if err != nil {
		return err
	}
	*i = T(n)
	return nil
}

func DecodeUvarint[T uint | uint16 | uint32 | uint64](buf *bytes.Reader, i *T) error {
	n, err := binary.ReadUvarint(buf)
	if err != nil {
		return err
	}
	*i = T(n)
	return nil
}

func EncodeInt16(buf *bytes.Buffer, i int16) error   { return EncodeVarint(buf, i) }
func EncodeInt32(buf *bytes.Buffer, i int32) error   { return EncodeVarint(buf, i) }
func EncodeInt64(buf *bytes.Buffer, i int64) error   { return EncodeVarint(buf, i) }
func EncodeUint16(buf *bytes.Buffer, i uint16) error { return EncodeUvarint(buf, i) }
func EncodeUint32(buf *bytes.Buffer, i uint32) error { return EncodeUvarint(buf, i) }
func EncodeUint64(buf *bytes.Buffer, i uint64) error { return EncodeUvarint(buf, i) }

func DecodeInt16(buf *bytes.Reader, i *int16) error   { return DecodeVarint(buf, i) }
func DecodeInt32(buf *bytes.Reader, i *int32) error   { return DecodeVarint(buf, i) }
func DecodeInt64(buf *bytes.Reader, i *int64) error   { return DecodeVarint(buf, i) }
func DecodeUint16(buf *bytes.Reader, i *uint16) error { return DecodeUvarint(buf, i) }
func DecodeUint32(buf *bytes.Reader, i *uint32) error { return DecodeUvarint(buf, i) }
func DecodeUint64(buf *bytes.Reader, i *uint64) error { return DecodeUvarint(buf, i) }

func EncodeInt(buf *bytes.Buffer, i int) error { return EncodeVarint(buf, int64(i)) }
func DecodeInt(buf *bytes.Reader, i *int) error {
	var i64 int64
	err := DecodeVarint(buf, &i64)
	if err != nil {
		return err
	}
	*i = int(i64)
	return nil
}

// Encode a struct. It uses type assert to leverage Gob to encode the value when
// the struct hasn't be converted to use codegen to generate encoding logic, this
// should be removed once all are converted.
func EncodeStruct(c EncContext, buf *bytes.Buffer, val any) error {
	// val is pointer to either a struct or an interface{}. If it is the latter the
	// type assert below will fail even if the underlying concrete type implements
	// the CustomEnc interface. This is intentional in order for ob to handle the
	// interface case, where it will store the interface info and is albe to properly
	// deserialize it later. Otherwise, it will be serialized as a concrete type,
	// then later it can't be deserialized back to an interface field.
	if encdec, ok := val.(CustomEnc); ok {
		return encdec.Encode(c, buf)
	} else {
		panic(fmt.Errorf("encoding type is not supported: %T", val))
	}
}

// Encode an interface value.
func EncodeInterface(c EncContext, buf *bytes.Buffer, data any) error {
	if data == nil {
		return EncodeInt16(buf, int16(nilInterface))
	}
	intfType := valueInterface
	if v := reflect.ValueOf(data); v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return fmt.Errorf("nil pointer is not supported in EncodeInterface")
		} else {
			intfType = pointerInterface
		}
	}
	if err := EncodeInt16(buf, int16(intfType)); err != nil {
		return err
	}
	val := data.(CustomEnc)
	if err := EncodeInt16(buf, val.GetTypeId()); err != nil {
		return err
	}
	return val.Encode(c, buf)
}

// Decode a struct. It uses type assert to leverage Gob to decode the value when
// the struct hasn't be converted to use codegen to generate decoding logic, this
// should be removed once all are converted
func DecodeStruct(c EncContext, buf *bytes.Reader, data any) error {
	if encdec, ok := data.(CustomDec); ok {
		return encdec.Decode(c, buf)
	} else {
		panic(fmt.Errorf("decoding type is not supported: %T", data))
	}
}

// Decode an interface value.
func DecodeInterface(c EncContext, buf *bytes.Reader) (any, error) {
	var intfType int16
	if err := DecodeInt16(buf, &intfType); err != nil || intfType == int16(nilInterface) {
		return nil, err
	}
	var typeId int16
	if err := DecodeInt16(buf, &typeId); err != nil {
		return nil, err
	}
	if f, ok := typeRegistry[typeId]; !ok {
		return nil, fmt.Errorf("type not registered: %d", typeId)
	} else {
		val := f()
		if err := val.Decode(c, buf); err != nil {
			return nil, err
		} else if intfType == int16(valueInterface) {
			return reflect.ValueOf(val).Elem().Interface(), nil
		} else {
			return val, nil
		}
	}
}
