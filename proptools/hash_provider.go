// Copyright 2023 Google Inc. All rights reserved.
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
	"cmp"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"math"
	"reflect"
	"slices"
	"strconv"
	"text/scanner"
	"unsafe"

	"github.com/google/blueprint/pool"
)

var hasherPool = pool.New[hasher]()

const HashSize = 8

type Hash [1]uint64

func (h *Hash) UnmarshalJSON(bytes []byte) error {
	var s []uint64
	err := json.Unmarshal(bytes, &s)
	if err != nil {
		return err
	}
	if len(s) != len(h) {
		return fmt.Errorf("expected %d elements, got %d", len(h), len(s))
	}
	copy(h[:], s)
	return nil
}

func (h *Hash) MarshalJSON() ([]byte, error) {
	return json.Marshal(h[:])
}

var ZeroHash Hash

func (h *Hash) FormatUint(base int) string {
	return strconv.FormatUint(h[0], base)
}

func (h *Hash) PutBigEndian(buf []byte) {
	binary.BigEndian.PutUint64(buf, h[0])
}

func (h *Hash) Bytes() []byte {
	ptr := unsafe.Pointer(unsafe.SliceData(h[:]))
	return unsafe.Slice((*byte)(ptr), len(h)*int(unsafe.Sizeof(h[0])))
}

func CalculateHash(value interface{}) (Hash, error) {
	hasher := hasherPool.Get()
	defer hasherPool.Put(hasher)
	hasher.reset()

	v := reflect.ValueOf(value)
	var err error
	if v.IsValid() {
		err = hasher.calculateHash(v)
	}
	return Hash{hasher.Sum64()}, err
}

type hasher struct {
	hash.Hash64
	int64Buf      [8]byte
	ptrs          map[uintptr]uint64
	visiting      map[uintptr]bool
	mapStateCache *mapState
}

// Preallocate the ptrs map in the hasher to a value slightly larger than the maximum number of pointers
// seen in a call to CalculateHash to avoid allocations.  The hasher objects are reused in a pool, so the
// total number of these maps will be small.
const ptrsMapSize = 16384

type mapState struct {
	indexes []int
	keys    []reflect.Value
	values  []reflect.Value
}

func (hasher *hasher) reset() {
	if hasher.Hash64 == nil {
		hasher.Hash64 = fnv.New64()
	} else {
		hasher.Hash64.Reset()
	}

	clear(hasher.ptrs)
	clear(hasher.visiting)
}

func (hasher *hasher) writeUint64(i uint64) {
	binary.LittleEndian.PutUint64(hasher.int64Buf[:], i)
	hasher.Write(hasher.int64Buf[:])
}

func (hasher *hasher) writeInt(i int) {
	hasher.writeUint64(uint64(i))
}

func (hasher *hasher) writeByte(i byte) {
	hasher.int64Buf[0] = i
	hasher.Write(hasher.int64Buf[:1])
}

func (hasher *hasher) writeString(s string) {
	strLen := len(s)
	if strLen == 0 {
		// unsafe.StringData is unspecified in this case
		hasher.writeByte(0)
		return
	}

	hasher.Write(unsafe.Slice(unsafe.StringData(s), strLen))
}

func (hasher *hasher) writeHash(h Hash) {
	for _, e := range h {
		hasher.writeUint64(e)
	}
}

func (hasher *hasher) getMapState(size int) *mapState {
	s := hasher.mapStateCache
	// Clear hasher.mapStateCache so that any recursive uses don't collide with this frame.
	hasher.mapStateCache = nil

	if s == nil {
		s = &mapState{}
	}

	// Reset the slices to length `size` and capacity at least `size`
	s.indexes = slices.Grow(s.indexes[:0], size)[0:size]
	s.keys = slices.Grow(s.keys[:0], size)[0:size]
	s.values = slices.Grow(s.values[:0], size)[0:size]

	return s
}

func (hasher *hasher) putMapState(s *mapState) {
	if hasher.mapStateCache == nil || cap(hasher.mapStateCache.indexes) < cap(s.indexes) {
		hasher.mapStateCache = s
	}
}

func (hasher *hasher) calculateHash(v reflect.Value) error {
	var h Hash
	var err error
	// Include the hash of the type so that hashes of types with the same contents, for example
	// empty slices of different types, produce different hashes.  The hash of each type is cached,
	// so this should be very fast.
	h, err = typeHash(v.Type())
	if err != nil {
		return err
	}
	hasher.writeHash(h)
	v.IsValid()
	switch v.Kind() {
	case reflect.Struct:
		// The scanner.Position is intentionally excluded from the hash calculation.
		// This field should only be used for printing user-facing error messages,
		// as it is sensitive to formatting changes like comments and whitespace.
		// Including it would cause the hash to change and trigger an unnecessary
		// re-analysis when no actual property has been modified.
		if v.Type() == reflect.TypeOf(scanner.Position{}) {
			return nil
		}
		l := v.NumField()
		hasher.writeInt(l)
		for i := 0; i < l; i++ {
			err := hasher.calculateHash(v.Field(i))
			if err != nil {
				return fmt.Errorf("in field %s: %s", v.Type().Field(i).Name, err.Error())
			}
		}
	case reflect.Map:
		l := v.Len()
		hasher.writeInt(l)
		iter := v.MapRange()
		s := hasher.getMapState(l)
		for i := 0; iter.Next(); i++ {
			s.indexes[i] = i
			s.keys[i] = iter.Key()
			s.values[i] = iter.Value()
		}
		slices.SortFunc(s.indexes, func(i, j int) int {
			return compare_values(s.keys[i], s.keys[j])
		})
		for i := 0; i < l; i++ {
			err := hasher.calculateHash(s.keys[s.indexes[i]])
			if err != nil {
				return fmt.Errorf("in map: %s", err.Error())
			}
			err = hasher.calculateHash(s.values[s.indexes[i]])
			if err != nil {
				return fmt.Errorf("in map: %s", err.Error())
			}
		}
		hasher.putMapState(s)
	case reflect.Slice, reflect.Array:
		l := v.Len()
		hasher.writeInt(l)
		for i := 0; i < l; i++ {
			err := hasher.calculateHash(v.Index(i))
			if err != nil {
				return fmt.Errorf("in %s at index %d: %s", v.Kind().String(), i, err.Error())
			}
		}
	case reflect.Pointer:
		if v.IsNil() {
			hasher.writeByte(0)
			return nil
		}
		addr := v.Pointer()
		if hasher.ptrs == nil {
			hasher.ptrs = make(map[uintptr]uint64, ptrsMapSize)
		}
		if hasher.visiting == nil {
			hasher.visiting = make(map[uintptr]bool, ptrsMapSize)
		}
		if _, ok := hasher.visiting[addr]; ok {
			// Circular dependency detected (we have this in Scope at least), just return nil for now.
			return nil
		}
		// The special logic below is to avoid hashing the same pointer more than once.
		// We store the current hash value, then reset the hasher to a clean state in
		// order to calculate the hash of the pointer which will be cached for future
		// encounters. Once we have the hash value of the pointer, we hash both the
		// stored hash value and the hash value of the pointer.
		prevHash := hasher.Sum64()
		hasher.Reset()

		ptrHash, ok := hasher.ptrs[addr]
		if !ok {
			hasher.visiting[addr] = true
			err := hasher.calculateHash(v.Elem())
			if err != nil {
				return fmt.Errorf("in pointer: %s", err.Error())
			}
			ptrHash = hasher.Sum64()
			hasher.ptrs[addr] = ptrHash
			delete(hasher.visiting, addr)
		}

		hasher.Reset()
		hasher.writeUint64(prevHash)
		hasher.writeUint64(ptrHash)

	case reflect.Interface:
		if v.IsNil() {
			hasher.writeByte(0)
		} else {
			// Include the hash of the type so that hashes of types with the same contents, for example
			// empty slices of different types, produce different hashes.  The hash of each type is cached,
			// so this should be very fast.
			h, err := typeHash(v.Elem().Type())
			if err != nil {
				return err
			}
			hasher.writeHash(h)
			// The only way get the pointer out of an interface to hash it or check for cycles
			// would be InterfaceData(), but that's deprecated and seems like it has undefined behavior.
			err = hasher.calculateHash(v.Elem())
			if err != nil {
				return fmt.Errorf("in interface: %s", err.Error())
			}
		}
	case reflect.String:
		hasher.writeString(v.String())
	case reflect.Bool:
		if v.Bool() {
			hasher.writeByte(1)
		} else {
			hasher.writeByte(0)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		hasher.writeUint64(v.Uint())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		hasher.writeUint64(uint64(v.Int()))
	case reflect.Float32, reflect.Float64:
		hasher.writeUint64(math.Float64bits(v.Float()))
	default:
		return fmt.Errorf("data may only contain primitives, strings, arrays, slices, structs, maps, and pointers, found: %s", v.Kind().String())
	}
	return nil
}

func compare_values(x, y reflect.Value) int {
	if x.Type() != y.Type() {
		panic("Expected equal types")
	}

	switch x.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmp.Compare(x.Uint(), y.Uint())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(x.Int(), y.Int())
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(x.Float(), y.Float())
	case reflect.String:
		return cmp.Compare(x.String(), y.String())
	case reflect.Bool:
		if x.Bool() == y.Bool() {
			return 0
		} else if x.Bool() {
			return 1
		} else {
			return -1
		}
	case reflect.Pointer:
		return cmp.Compare(x.Pointer(), y.Pointer())
	case reflect.Array:
		l := x.Len()
		for i := 0; i < l; i++ {
			if result := compare_values(x.Index(i), y.Index(i)); result != 0 {
				return result
			}
		}
		return 0
	case reflect.Struct:
		l := x.NumField()
		for i := 0; i < l; i++ {
			if result := compare_values(x.Field(i), y.Field(i)); result != 0 {
				return result
			}
		}
		return 0
	case reflect.Interface:
		if x.IsNil() && y.IsNil() {
			return 0
		} else if x.IsNil() {
			return 1
		} else if y.IsNil() {
			return -1
		}
		return compare_values(x.Elem(), y.Elem())
	default:
		panic(fmt.Sprintf("Could not compare types %s and %s", x.Type().String(), y.Type().String()))
	}
}

func ContainsConfigurable(value interface{}) bool {
	ptrs := make(map[uintptr]bool)
	v := reflect.ValueOf(value)
	if v.IsValid() {
		return containsConfigurableInternal(v, ptrs)
	}
	return false
}

func containsConfigurableInternal(v reflect.Value, ptrs map[uintptr]bool) bool {
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		if IsConfigurable(t) {
			return true
		}
		typeFields := typeFields(t)
		for i := 0; i < v.NumField(); i++ {
			if HasTag(typeFields[i], "blueprint", "allow_configurable_in_provider") {
				continue
			}
			if containsConfigurableInternal(v.Field(i), ptrs) {
				return true
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key()
			value := iter.Value()
			if containsConfigurableInternal(key, ptrs) {
				return true
			}
			if containsConfigurableInternal(value, ptrs) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		l := v.Len()
		for i := 0; i < l; i++ {
			if containsConfigurableInternal(v.Index(i), ptrs) {
				return true
			}
		}
	case reflect.Pointer:
		if v.IsNil() {
			return false
		}
		addr := v.Pointer()
		if _, ok := ptrs[addr]; ok {
			// pointer cycle
			return false
		}
		ptrs[addr] = true
		if containsConfigurableInternal(v.Elem(), ptrs) {
			return true
		}
	case reflect.Interface:
		if v.IsNil() {
			return false
		} else {
			// The only way get the pointer out of an interface to hash it or check for cycles
			// would be InterfaceData(), but that's deprecated and seems like it has undefined behavior.
			if containsConfigurableInternal(v.Elem(), ptrs) {
				return true
			}
		}
	default:
		return false
	}
	return false
}
