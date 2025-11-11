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

	"github.com/google/blueprint/syncmap"
)

var typeHashCache syncmap.SyncMap[reflect.Type, Hash]

// TypeHash computes a hash for a reflect.Type that is stable across
// multiple runs of the same binary, and should return identical values
// for the same reflect.Type and different values for different reflect.Type,
// except that types constructed at runtime, for example with reflect.StructOf,
// will give identical hashes if constructed from identical inputs.  Since the
// hash of a type is constant the result is cached, so calling this method will
// be very fast on average.
func TypeHash(typ reflect.Type) (Hash, error) {
	// Fast path, attempt to load from cache.
	if h, ok := typeHashCache.Load(typ); ok {
		return h, nil
	}

	// Slow path, compute the hash.
	h, err := typeHashSlow(typ)
	if err != nil {
		return Hash{}, err
	}

	// Insert the generated hash into the cache
	h, _ = typeHashCache.LoadOrStore(typ, h)

	return h, nil
}

// typeHashSlow computes the hash for a type without caching.
func typeHashSlow(typ reflect.Type) (Hash, error) {
	hasher := hasherPool.Get()
	defer hasherPool.Put(hasher)
	hasher.reset()
	// Hash the package path to ensure types with the same package short name
	// are different.
	hasher.WriteString(typ.PkgPath())
	hasher.WriteString(typ.String())
	return Hash{hasher.Sum64()}, nil
}
