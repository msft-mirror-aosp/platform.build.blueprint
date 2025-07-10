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
	"path/filepath"
	"reflect"

	"github.com/akrylysov/pogreb"
	"github.com/google/blueprint/proptools"
	"github.com/google/blueprint/syncmap"
)

const dbName = "references.db"

type KeyValueStore interface {
	Put(key []byte, value []byte) error
	Get(key []byte) ([]byte, error)
	Close() error
}

type InMemKeyValueStore struct {
	data syncmap.SyncMap[string, []byte]
}

func (s *InMemKeyValueStore) Close() error {
	return nil
}

func (s *InMemKeyValueStore) Put(key []byte, value []byte) error {
	s.data.LoadOrStore(string(key), value)
	return nil
}

func (s *InMemKeyValueStore) Get(key []byte) ([]byte, error) {
	if ret, ok := s.data.Load(string(key)); !ok {
		return nil, nil
	} else {
		return ret, nil
	}
}

type EncContext interface {
	ReferenceEnc
}

type ReferenceEnc interface {
	EncodeReferences() error
	Close()
	EncodeReference(value any, buf *bytes.Buffer, typ string, encode func(value any, buf *bytes.Buffer) error) error
	DecodeReference(buf *bytes.Reader, decode func(buf *bytes.Reader) (any, error)) (any, error)
}

func NewEncContext(dbPath string) EncContext {
	return NewReferencesEncoder(dbPath)
}

type ReferencesEncoder struct {
	encodedReferences syncmap.SyncMap[any, *encodedReference]
	decodedReferences syncmap.SyncMap[uint64, any]
	db                KeyValueStore
}

// NewReferencesEncoder creates and initializes a new ReferencesEncoder.
func NewReferencesEncoder(dbPath string) *ReferencesEncoder {
	ctx := &ReferencesEncoder{}
	ctx.open(dbPath)
	return ctx
}

func NewReferencesEncoderForTest() *ReferencesEncoder {
	ctx := &ReferencesEncoder{}
	ctx.openForTests()
	return ctx
}

// encodedReference stores information about an encoded value reference.
type encodedReference struct {
	valueRefId          uint64        // The unique hash ID for the value.
	valueEncodingBuffer *bytes.Buffer // The buffer containing the encoded actual value (including its own ref ID and length).
}

func (b *ReferencesEncoder) openForTests() error {
	if b.db != nil {
		panic(fmt.Errorf("db is already open"))
	}

	b.db = &InMemKeyValueStore{}
	return nil
}

func (b *ReferencesEncoder) open(dbPath string) error {
	if b.db != nil {
		panic(fmt.Errorf("db is already open"))
	}
	db, err := pogreb.Open(filepath.Join(dbPath, dbName), nil)
	if err != nil {
		return err
	}
	b.db = db
	return nil
}

func (b *ReferencesEncoder) Close() {
	b.db.Close()
}

func (c *ReferencesEncoder) EncodeReferences() error {
	var err error
	c.encodedReferences.Range(func(_ any, value *encodedReference) bool {
		if err = c.db.Put(uint64ToBytes(value.valueRefId), value.valueEncodingBuffer.Bytes()); err != nil {
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
		ref, err := proptools.CalculateHash(valueHashConfig{
			typ:   typ,
			value: value,
		})
		if err != nil {
			return err
		}

		// Encode the calculated reference ID into the main buffer (where the value's full encoded data will reside).
		if err := EncodeSimple(buf, ref); err != nil {
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
	return EncodeSimple(buf, encStruct.valueRefId)
}

func (c *ReferencesEncoder) DecodeReference(buf *bytes.Reader, decode func(buf *bytes.Reader) (any, error)) (any, error) {
	var ref uint64 // Variable to store the decoded reference ID.

	// Decode the reference ID of the value from the input stream.
	if err := DecodeSimple[uint64](buf, &ref); err != nil {
		return nil, err // Return error if decoding the reference fails.
	}

	// Try to load the value using its reference ID from the decoded values cache.
	if v, ok := c.decodedReferences.Load(ref); !ok {
		data, err := c.db.Get(uint64ToBytes(ref))
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

func uint64ToBytes(value uint64) []byte {
	ret := make([]byte, 8)
	binary.BigEndian.PutUint64(ret, value)
	return ret
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
		if err := val.Decode(c, buf); err != nil {
			return nil, err
		} else if intfType == valueInterface {
			return reflect.ValueOf(val).Elem().Interface(), nil
		} else {
			return val, nil
		}
	}
}
