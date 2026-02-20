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

package syncmap

import "sync"

// SyncMap is a wrapper around sync.Map that provides type safety via generics.
// It also has an additional LoadOrCompute() method that can be used to initialize a key only once.
type SyncMap[K comparable, V any] struct {
	sync.Map
}

type syncmapWaiter[V any] struct {
	value func() V
}

// Load returns the value stored in the map for a key, or the zero value if no
// value is present.
// The ok result indicates whether value was found in the map.
func (m *SyncMap[K, V]) Load(key K) (value V, ok bool) {
	v, ok := m.Map.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	if waiter, ok := v.(*syncmapWaiter[V]); ok {
		return waiter.value(), true
	}
	return v.(V), true
}

// Store sets the value for a key.
func (m *SyncMap[K, V]) Store(key K, value V) {
	m.Map.Store(key, value)
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (m *SyncMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	v, loaded := m.Map.LoadOrStore(key, value)
	if waiter, ok := v.(*syncmapWaiter[V]); ok {
		return waiter.value(), loaded
	}
	return v.(V), loaded
}

// LoadOrCompute returns the existing value for the key if present.
// Otherwise, it calls the computer function and stores and returns its result.
// The loaded result is true if the value was loaded, false if computed and stored.
// Any other operations that read from the map will wait for the computation to finish for
// any keys they read.
func (m *SyncMap[K, V]) LoadOrCompute(key K, computer func() V) (actual V, loaded bool) {
	waiter := &syncmapWaiter[V]{value: sync.OnceValue(computer)}
	v, loaded := m.Map.LoadOrStore(key, waiter)
	if waiter, ok := v.(*syncmapWaiter[V]); ok {
		value := waiter.value()
		// Replace the waiter with the real value to save some memory. Use CompareAndSwap
		// so that if we delete or overwrite the key we won't accidentally store the value again.
		if !loaded {
			m.Map.CompareAndSwap(key, waiter, value)
		}
		return value, loaded
	}
	return v.(V), loaded
}

func (m *SyncMap[K, V]) Range(f func(key K, value V) bool) {
	m.Map.Range(func(k, v any) bool {
		if waiter, ok := v.(*syncmapWaiter[V]); ok {
			return f(k.(K), waiter.value())
		}
		return f(k.(K), v.(V))
	})
}

func (m *SyncMap[K, V]) Delete(key K) {
	m.Map.Delete(key)
}
