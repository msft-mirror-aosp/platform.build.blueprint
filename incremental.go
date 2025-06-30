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

package blueprint

import (
	"bytes"
	"fmt"

	"github.com/google/blueprint/gobtools"
)

//go:generate go run gobtools/codegen/gob_gen.go

const dbName = "incremental.db"

// @auto-generate: gob
type BuildActionCacheKey struct {
	Id string
}

func (k *BuildActionCacheKey) bytes(ctx gobtools.EncContext) []byte {
	buf := &bytes.Buffer{}
	if err := k.Encode(ctx, buf); err != nil {
		panic(fmt.Errorf("failed to encode BuildActionCacheKey: %v", err))
	}
	return buf.Bytes()
}

// @auto-generate: gob
type CachedProvider struct {
	Id    *providerKey
	Value *any
}

// @auto-generate: gob
type BuildActionCachedData struct {
	InputHash        uint64
	Providers        []CachedProvider
	OrderOnlyStrings []string
	GlobCache        []globResultCache
}

type BuildActionCache struct {
	db gobtools.KeyValueStore
}

func (b *BuildActionCache) openForTests() error {
	if b.db != nil {
		panic(fmt.Errorf("db is already open"))
	}

	b.db = &gobtools.InMemKeyValueStore{}
	return nil
}

func (b *BuildActionCache) open(dbPath string) error {
	// Uncomment the code below once we support pogreb.
	/*
		if b.db != nil {
			panic(fmt.Errorf("db is already open"))
		}
		db, err := pogreb.Open(filepath.Join(dbPath, dbName), nil)
		if err != nil {
			return err
		}
		b.db = db
		return nil
	*/
	panic(fmt.Errorf("db support is not ready yet"))
}

func (b *BuildActionCache) read(ctx gobtools.EncContext, key *BuildActionCacheKey) (*BuildActionCachedData, error) {
	v, err := b.db.Get(key.bytes(ctx))
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}

	buf := bytes.NewReader(v)
	var ret BuildActionCachedData
	err = ret.Decode(ctx, buf)
	if err != nil {
		return nil, err
	}
	return &ret, nil
}

func (b *BuildActionCache) write(ctx gobtools.EncContext, key *BuildActionCacheKey, data *BuildActionCachedData) error {
	buf := &bytes.Buffer{}
	err := data.Encode(ctx, buf)
	if err != nil {
		return err
	}
	err = b.db.Put(key.bytes(ctx), buf.Bytes())
	if err != nil {
		return err
	}
	return nil
}

// @auto-generate: gob
type OrderOnlyStringsCache map[string][]string

type BuildActionCacheInput struct {
	PropertiesHash uint64
	ProvidersHash  [][]uint64
}

type Incremental interface {
	IncrementalSupported() bool
}

type IncrementalModule struct{}

func (m *IncrementalModule) IncrementalSupported() bool {
	return true
}
