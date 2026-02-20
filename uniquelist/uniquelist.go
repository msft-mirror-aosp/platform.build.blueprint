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

package uniquelist

import (
	"hash/maphash"
	"iter"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"time"
	"weak"

	"github.com/google/blueprint/proptools"
	"github.com/google/blueprint/syncmap"
)

// UniqueList is a workaround for Go limitation that slices are not comparable and
// thus can't be used with unique.Make.  It interns slices by manually hashing the
// contents of each element and using the result as the key in a sync.Map.
type UniqueList[T comparable] struct {
	p *[]T
}

// uniqueListMapsByType stores a map from the type of list element to the uniqueListMap
// that stores lists of that type.  The value in the map is always a *uniqueListMap[T]
// when the key is the reflect.TypeOf(T).
var uniqueListMapsByType syncmap.SyncMap[reflect.Type, any]

// uniqueListMap stores a map of hash of the contents of a slice to a weak pointer to
// a canonical slice with that contents.
type uniqueListMap[T comparable] = syncmap.SyncMap[uint64, weak.Pointer[[]T]]

// freeUnusedList stores a list of functions to call periodically to remove entries
// in uniqueListMaps whose weak pointer is no longer valid.
var freeUnusedList []func()

// freeUnusedMutex protects freeUnusedList.
var freeUnusedMutex sync.Mutex

// initFreeUnused creates a goroutine to call the functions in the freeUnusedList.
var initFreeUnused = sync.OnceFunc(func() {
	t := time.Tick(5 * time.Second)
	go func() {
		for {
			<-t
			freeUnusedMutex.Lock()
			c := freeUnusedList
			freeUnusedMutex.Unlock()

			for _, f := range c {
				f()
			}
		}
	}()
})

// Len returns the length of the slice that was originally passed to Make.
func (s UniqueList[T]) Len() int {
	if s.p == nil {
		return 0
	}
	return len(*s.p)
}

// ToSlice returns a slice containing a shallow copy of the list.
func (s UniqueList[T]) ToSlice() []T {
	if s.p == nil {
		return []T(nil)
	}
	return slices.Clone(*s.p)
}

// Iter returns a iter.Seq that iterates the elements of the list.
func (s UniqueList[T]) Iter() iter.Seq[T] {
	if s.p == nil {
		return func(yield func(T) bool) {}
	}
	return slices.Values(*s.p)
}

// AppendTo appends the contents of the list to the given slice and returns
// the results.
func (s UniqueList[T]) AppendTo(slice []T) []T {
	if s.p == nil {
		return slice
	}
	slice = append(slice, *s.p...)
	return slice
}

func (s UniqueList[T]) Hash(hasher *proptools.Hasher, typeName string, hashT func(*proptools.Hasher, T) error) error {
	if s.p == nil {
		hasher.WriteByte(0)
		return nil
	}
	hash := func(hasher *proptools.Hasher) error {
		hasher.WriteString("[]" + typeName)
		items := s.ToSlice()
		hasher.WriteInt(len(items))
		for i := 0; i < len(items); i++ {
			if err := hashT(hasher, items[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return proptools.HashReference(hasher, s, hash)
}

// Make returns a UniqueList for the given slice.  Two calls to UniqueList with the same slice contents
// will return identical UniqueList objects.
func Make[T comparable](slice []T) UniqueList[T] {
	if len(slice) == 0 {
		return UniqueList[T]{}
	}

	uniqueListsForT := getUniqueListMapForType[T]()
	key := hashSliceContents(slice)

	for {
		var s []T
		w, _ := uniqueListsForT.LoadOrCompute(key, func() weak.Pointer[[]T] {
			s = slices.Clone(slice)
			return weak.Make(&s)
		})

		p := w.Value()
		runtime.KeepAlive(s)
		if p != nil {
			return UniqueList[T]{p}
		}

		uniqueListsForT.CompareAndDelete(key, w)
	}
}

var seed = maphash.MakeSeed()

// hashSliceContents uses maphash.Hash to hash each element of a slice.
func hashSliceContents[T comparable](list []T) uint64 {
	var h maphash.Hash
	h.SetSeed(seed)
	for _, e := range list {
		maphash.WriteComparable(&h, e)
	}
	return h.Sum64()
}

// getUniqueListMapForType
func getUniqueListMapForType[T comparable]() *uniqueListMap[T] {
	var zero T
	typ := reflect.TypeOf(zero)

	initFreeUnused()
	uniqueListsForT, ok := uniqueListMapsByType.Load(typ)
	if !ok {
		var loaded bool
		uniqueListsForT = &uniqueListMap[T]{}
		uniqueListsForT, loaded = uniqueListMapsByType.LoadOrStore(typ, uniqueListsForT)
		if !loaded {
			u := uniqueListsForT.(*uniqueListMap[T])
			freeUnusedMutex.Lock()
			freeUnusedList = append(freeUnusedList, func() { freeUnused(u) })
			freeUnusedMutex.Unlock()
		}
	}
	return uniqueListsForT.(*uniqueListMap[T])
}

// freeUnused walks the entries in a uniqueListMap and removes any whose
// value is a weak pointer to an object that has been reclaimed.
func freeUnused[T comparable](u *uniqueListMap[T]) {
	u.Range(func(key uint64, value weak.Pointer[[]T]) bool {
		if value.Value() == nil {
			u.Delete(key)
		}
		return true
	})
}
