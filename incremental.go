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
	"errors"
	"fmt"
	"path/filepath"

	"github.com/akrylysov/pogreb"
	"github.com/google/blueprint/dbtools"
	"github.com/google/blueprint/gobtools"
)

//go:generate go run gobtools/codegen/gob_gen.go

const buildActionDbName = "incremental.db"
const providerDbName = "providers.db"
const referencesDbName = "references.db"

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
type ProviderCachedData struct {
	Providers []CachedProvider
}

// @auto-generate: gob
type ProviderHash struct {
	Id   *providerKey
	Hash uint64
}

// @auto-generate: gob
type BuildActionCachedData struct {
	InputHash        uint64
	ProviderHashes   []ProviderHash
	OrderOnlyStrings []string
	GlobCache        []globResultCache
}

type BuildActionCache struct {
	buildActionDb dbtools.KeyValueStore
	// Use a separate DB for providers so that we only read them when necessary.
	providerDb   dbtools.KeyValueStore
	referencesDb dbtools.KeyValueStore
}

func (b *BuildActionCache) openForTests() error {
	if b.buildActionDb != nil || b.providerDb != nil {
		panic(fmt.Errorf("db is already open"))
	}

	b.buildActionDb = &dbtools.InMemKeyValueStore{}
	b.providerDb = &dbtools.InMemKeyValueStore{}
	b.referencesDb = &dbtools.InMemKeyValueStore{}
	return nil
}

func (b *BuildActionCache) open(dbPath string) error {
	if b.buildActionDb != nil || b.providerDb != nil || b.referencesDb != nil {
		panic(fmt.Errorf("db is already open"))
	}
	db, err := pogreb.Open(filepath.Join(dbPath, buildActionDbName), nil)
	if err != nil {
		return err
	}
	b.buildActionDb = db

	db, err = pogreb.Open(filepath.Join(dbPath, providerDbName), nil)
	if err != nil {
		return err
	}
	b.providerDb = db

	db, err = pogreb.Open(filepath.Join(dbPath, referencesDbName), nil)
	if err != nil {
		return err
	}
	b.referencesDb = db

	return nil
}

func (b *BuildActionCache) close() error {
	return errors.Join(
		b.buildActionDb.Close(),
		b.providerDb.Close(),
		b.referencesDb.Close())
}

func (b *BuildActionCache) readBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey) (*BuildActionCachedData, error) {
	var ret BuildActionCachedData
	if err := read(ctx, b.buildActionDb, key, &ret); err != nil {
		return nil, err
	}
	return &ret, nil
}

func (b *BuildActionCache) readProviders(ctx gobtools.EncContext, key *BuildActionCacheKey) (*ProviderCachedData, error) {
	var ret ProviderCachedData
	if err := read(ctx, b.providerDb, key, &ret); err != nil {
		return nil, err
	}
	return &ret, nil
}

func read(ctx gobtools.EncContext, db dbtools.KeyValueStore, key *BuildActionCacheKey, ret gobtools.CustomDec) error {
	v, err := db.Get(key.bytes(ctx))
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}

	buf := bytes.NewReader(v)
	return ret.Decode(ctx, buf)
}

func (b *BuildActionCache) writeBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey, data *BuildActionCachedData) error {
	return write(ctx, b.buildActionDb, key, data)
}

func (b *BuildActionCache) writeProviders(ctx gobtools.EncContext, key *BuildActionCacheKey, data *ProviderCachedData) error {
	return write(ctx, b.providerDb, key, data)
}

func write(ctx gobtools.EncContext, db dbtools.KeyValueStore, key *BuildActionCacheKey, data gobtools.CustomEnc) error {
	buf := &bytes.Buffer{}
	err := data.Encode(ctx, buf)
	if err != nil {
		return err
	}
	err = db.Put(key.bytes(ctx), buf.Bytes())
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
