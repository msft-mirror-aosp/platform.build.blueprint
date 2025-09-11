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

const moduleActionsDbName = "module_actions.db"
const providersDbName = "providers.db"
const referencesDbName = "references.db"
const ninjaDbName = "ninja.db"

// @auto-generate: gob
type BuildActionCacheKey struct {
	Id string
}

func (k *BuildActionCacheKey) bytes() []byte {
	return []byte(k.Id)
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
type ModuleActionCachedData struct {
	InputHash        uint64
	ProviderHashes   []ProviderHash
	OrderOnlyStrings []string
	GlobCache        []globResultCache
}

type BuildActionCache struct {
	moduleActionsDb dbtools.KeyValueStore
	// Use a separate DB for providers so that we only read them when necessary.
	providerDb   dbtools.KeyValueStore
	referencesDb dbtools.KeyValueStore
	ninjaDb      dbtools.KeyValueStore
}

func (b *BuildActionCache) openForTests() error {
	b.moduleActionsDb = &dbtools.InMemKeyValueStore{}
	b.providerDb = &dbtools.InMemKeyValueStore{}
	b.referencesDb = &dbtools.InMemKeyValueStore{}
	b.ninjaDb = &dbtools.InMemKeyValueStore{}
	return nil
}

func (b *BuildActionCache) open(dbPath string) error {
	if b.moduleActionsDb != nil || b.providerDb != nil || b.referencesDb != nil {
		panic(fmt.Errorf("db is already open"))
	}
	db, err := pogreb.Open(filepath.Join(dbPath, moduleActionsDbName), nil)
	if err != nil {
		return err
	}
	b.moduleActionsDb = db

	db, err = pogreb.Open(filepath.Join(dbPath, providersDbName), nil)
	if err != nil {
		return err
	}
	b.providerDb = db

	db, err = pogreb.Open(filepath.Join(dbPath, referencesDbName), nil)
	if err != nil {
		return err
	}
	b.referencesDb = db

	db, err = pogreb.Open(filepath.Join(dbPath, ninjaDbName), nil)
	if err != nil {
		return err
	}
	b.ninjaDb = db

	return nil
}

func (b *BuildActionCache) close() error {
	return errors.Join(
		b.moduleActionsDb.Close(),
		b.providerDb.Close(),
		b.referencesDb.Close(),
		b.ninjaDb.Close())
}

func (b *BuildActionCache) reset(c *Context, dbPath string) error {
	return errors.Join(
		c.fs.Remove(filepath.Join(dbPath, moduleActionsDbName)),
		c.fs.Remove(filepath.Join(dbPath, providersDbName)),
		c.fs.Remove(filepath.Join(dbPath, referencesDbName)),
		c.fs.Remove(filepath.Join(dbPath, ninjaDbName)))
}

func (b *BuildActionCache) readModuleBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey) (*ModuleActionCachedData, error) {
	var ret ModuleActionCachedData
	if err := read(ctx, b.moduleActionsDb, key, &ret); err != nil {
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

func (b *BuildActionCache) readNinjaStatements(key *BuildActionCacheKey) ([]byte, error) {
	return b.ninjaDb.Get(key.bytes())
}

func read(ctx gobtools.EncContext, db dbtools.KeyValueStore, key *BuildActionCacheKey, ret gobtools.CustomDec) error {
	v, err := db.Get(key.bytes())
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}

	buf := bytes.NewReader(v)
	return ret.Decode(ctx, buf)
}

func (b *BuildActionCache) writeModuleBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey, data *ModuleActionCachedData) error {
	return write(ctx, b.moduleActionsDb, key, data)
}

func (b *BuildActionCache) writeProviders(ctx gobtools.EncContext, key *BuildActionCacheKey, data *ProviderCachedData) error {
	return write(ctx, b.providerDb, key, data)
}

func (b *BuildActionCache) writeNinjaStatements(key *BuildActionCacheKey, data []byte) error {
	return b.ninjaDb.Put(key.bytes(), data)
}

func write(ctx gobtools.EncContext, db dbtools.KeyValueStore, key *BuildActionCacheKey, data gobtools.CustomEnc) error {
	buf := &bytes.Buffer{}
	err := data.Encode(ctx, buf)
	if err != nil {
		return err
	}
	err = db.Put(key.bytes(), buf.Bytes())
	if err != nil {
		return err
	}
	return nil
}

// @auto-generate: gob
type OrderOnlyStringsCache map[string][]string

type ModuleBuildActionCacheInput struct {
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
