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
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"github.com/akrylysov/pogreb"

	"github.com/google/blueprint/dbtools"
	"github.com/google/blueprint/gobtools"
	"github.com/google/blueprint/pool"
	"github.com/google/blueprint/proptools"
	"github.com/google/blueprint/syncmap"
)

//go:generate go run ./gobtools/codegen

const moduleActionsDbName = "module_actions.db"
const singletonActionsDbName = "singleton_actions.db"
const providersDbName = "providers.db"
const referencesDbName = "references.db"
const ninjaDbName = "ninja.db"

var IncrementalInfoDbNames = []string{
	moduleActionsDbName,
	singletonActionsDbName,
	providersDbName,
	referencesDbName,
	ninjaDbName,
}

// @auto-generate: gob
type BuildActionCacheKey struct {
	Id string
}

func (k *BuildActionCacheKey) bytes() []byte {
	return unsafe.Slice(unsafe.StringData(k.Id), len(k.Id))
}

type ProviderCacheKey struct {
	BuildActionCacheKey
	ProviderId int
}

func (k *ProviderCacheKey) bytes() []byte {
	buf := make([]byte, len(k.BuildActionCacheKey.Id), len(k.BuildActionCacheKey.Id)+8)
	copy(buf, k.BuildActionCacheKey.bytes())
	buf = binary.LittleEndian.AppendUint64(buf, uint64(k.ProviderId))
	return buf
}

// @auto-generate: gob
type CachedProvider struct {
	Id    *providerKey
	Value any
}

// @auto-generate: gob
type ProviderHash struct {
	Id   *providerKey
	Hash proptools.Hash
}

// @auto-generate: gob
type ModuleActionCachedData struct {
	InputHash        proptools.Hash
	ProviderHashes   []ProviderHash
	OrderOnlyStrings []string
	GlobCache        []globResultCache
}

// @auto-generate: gob
type SingletonActionCachedData struct {
	ProviderHashes           []ProviderHash
	DependencyProviderHashes map[int]proptools.Hash
}

// A dbWriteRequest is passed to providerDbWriter() through writerCh to write a key-value pair to a database.
type dbWriteRequest struct {
	key   proptools.Hash
	value *bytes.Buffer
}

type BuildActionCache struct {
	moduleActionsDb    dbtools.KeyValueStore
	singletonActionsDb dbtools.KeyValueStore
	// Use a separate DB for providers so that we only read them when necessary.
	providerDb   dbtools.KeyValueStore
	referencesDb dbtools.KeyValueStore
	ninjaDb      dbtools.KeyValueStore

	writerCh   chan dbWriteRequest
	writerDone chan bool

	hashesInProviderDb   map[proptools.Hash]struct{}
	cachedProviderHashes syncmap.SyncMap[proptools.Hash, CachedProvider]
}

var bufferPool = pool.New[bytes.Buffer]()

func (b *BuildActionCache) openForTests() error {
	b.hashesInProviderDb = make(map[proptools.Hash]struct{})
	b.providerDbWriter()
	b.moduleActionsDb = &dbtools.InMemKeyValueStore{}
	b.singletonActionsDb = &dbtools.InMemKeyValueStore{}
	b.providerDb = &dbtools.InMemKeyValueStore{}
	b.referencesDb = &dbtools.InMemKeyValueStore{}
	b.ninjaDb = &dbtools.InMemKeyValueStore{}
	return nil
}

func (b *BuildActionCache) open(dbPath string) error {
	b.hashesInProviderDb = make(map[proptools.Hash]struct{})
	b.providerDbWriter()
	return errors.Join(
		openDb(dbPath, moduleActionsDbName, &b.moduleActionsDb),
		openDb(dbPath, singletonActionsDbName, &b.singletonActionsDb),
		openDb(dbPath, providersDbName, &b.providerDb),
		openDb(dbPath, referencesDbName, &b.referencesDb),
		openDb(dbPath, ninjaDbName, &b.ninjaDb),
	)
}

// providerDbWriter starts a background goroutine that takes write requests from
// b.writerCh and writes them to the database.  It avoids lock contention
// on the database by moving all writes into a single goroutine, and verifies
// that a given provider hash is only written once.
func (b *BuildActionCache) providerDbWriter() {
	b.writerCh = make(chan dbWriteRequest, 1000)
	b.writerDone = make(chan bool)
	go func() {
		defer close(b.writerDone)
		for req := range b.writerCh {
			b.handleDbWriteRequest(req)
		}
	}()
}

func (b *BuildActionCache) handleDbWriteRequest(req dbWriteRequest) {
	// The request contains a buffer in req.value that should be returned to the pool.
	defer bufferPool.Put(req.value)

	if _, stored := b.hashesInProviderDb[req.key]; stored {
		return
	}
	b.hashesInProviderDb[req.key] = struct{}{}
	err := b.providerDb.Put(req.key.Bytes(), req.value.Bytes())
	if err != nil {
		panic(err)
	}
}

func openDb(dbPath string, dbName string, dbToOpen *dbtools.KeyValueStore) error {
	if *dbToOpen != nil {
		panic(fmt.Errorf("db %s is already open", dbName))
	}
	db, err := pogreb.Open(filepath.Join(dbPath, dbName), nil)
	if err != nil {
		return err
	}
	*dbToOpen = db
	return nil
}

func (b *BuildActionCache) flush() {
	// Close the writerCh.  Any calls to write() concurrent with the call to flush() may panic.
	close(b.writerCh)
	// Wait for the providerDbWriter goroutine to finish.
	<-b.writerDone
	// Restart the providerDbWriter goroutine
	b.providerDbWriter()
}

func (b *BuildActionCache) close() error {
	// Close the writerCh.  Any calls to write() after this will panic.
	close(b.writerCh)
	// Wait for the providerDbWriter goroutine to finish.
	<-b.writerDone
	return errors.Join(
		b.moduleActionsDb.Close(),
		b.singletonActionsDb.Close(),
		b.providerDb.Close(),
		b.referencesDb.Close(),
		b.ninjaDb.Close())
}

func (b *BuildActionCache) reset(c *Context, dbPath string) error {
	c.BeginEvent("reset_build_action_cache")
	defer c.EndEvent("reset_build_action_cache")

	return errors.Join(
		c.fs.Remove(filepath.Join(dbPath, moduleActionsDbName)),
		c.fs.Remove(filepath.Join(dbPath, singletonActionsDbName)),
		c.fs.Remove(filepath.Join(dbPath, providersDbName)),
		c.fs.Remove(filepath.Join(dbPath, referencesDbName)),
		c.fs.Remove(filepath.Join(dbPath, ninjaDbName)))
}

func (b *BuildActionCache) readModuleBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey) (*ModuleActionCachedData, error) {
	var ret ModuleActionCachedData
	if ok, err := read(ctx, b.moduleActionsDb, key.bytes(), &ret); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}
	return &ret, nil
}

func (b *BuildActionCache) readNinjaStatements(key *BuildActionCacheKey) ([]byte, error) {
	return b.ninjaDb.Get(key.bytes())
}

func (b *BuildActionCache) readSingletonBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey) (*SingletonActionCachedData, error) {
	var ret SingletonActionCachedData
	if ok, err := read(ctx, b.singletonActionsDb, key.bytes(), &ret); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}
	return &ret, nil
}

func (b *BuildActionCache) readProvider(ctx gobtools.EncContext, hash proptools.Hash, provider *providerKey) (CachedProvider, error) {
	checkProvider := func(ret CachedProvider) {
		if *ret.Id != *provider {
			panic(fmt.Errorf("restored provider %#v but got provider %#v", provider, ret.Id))
		}
	}

	// Return the copy cached in memory if it has already been read from the disk cache.
	if cached, ok := b.cachedProviderHashes.Load(hash); ok {
		checkProvider(cached)
		return cached, nil
	}

	// Read from the disk cache.
	var ret CachedProvider
	ok, err := read(ctx, b.providerDb, hash.Bytes(), &ret)
	if err != nil {
		return CachedProvider{}, err
	} else if !ok {
		return CachedProvider{}, nil
	}

	checkProvider(ret)

	// Insert into the in-memory cache.
	ret, loaded := b.cachedProviderHashes.LoadOrStore(hash, ret)
	if loaded {
		checkProvider(ret)
	}

	return ret, nil
}

func read(ctx gobtools.EncContext, db dbtools.KeyValueStore, key []byte, ret gobtools.CustomDec) (bool, error) {
	v, err := db.Get(key)
	if err != nil {
		return false, err
	}
	if v == nil {
		return false, nil
	}

	buf := bytes.NewReader(v)
	return true, ret.Decode(ctx, buf)
}

func (b *BuildActionCache) writeModuleBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey, data *ModuleActionCachedData) error {
	return b.write(ctx, b.moduleActionsDb, key.bytes(), data)
}

func (b *BuildActionCache) writeSingletonBuildAction(ctx gobtools.EncContext, key *BuildActionCacheKey, data *SingletonActionCachedData) error {
	return b.write(ctx, b.singletonActionsDb, key.bytes(), data)
}

func (b *BuildActionCache) writeProvider(ctx gobtools.EncContext, hash proptools.Hash, provider CachedProvider) error {
	buf := bufferPool.Get()
	buf.Reset()

	err := provider.Encode(ctx, buf)
	if err != nil {
		bufferPool.Put(buf)
		return err
	}

	// The buffer is transferred to the dbWriteRequest, so it must not be returned to the pool.
	b.writerCh <- dbWriteRequest{
		key:   hash,
		value: buf,
	}
	return nil
}

func (b *BuildActionCache) writeNinjaStatements(key *BuildActionCacheKey, data []byte) error {
	return b.ninjaDb.Put(key.bytes(), data)
}

// write encodes data to a byte buffer, and then sends a write request to the providerDbWriter goroutine to write it to the
// database.
func (b *BuildActionCache) write(ctx gobtools.EncContext, db dbtools.KeyValueStore, key []byte, data gobtools.CustomEnc) error {
	buf := bufferPool.Get()
	defer bufferPool.Put(buf)
	buf.Reset()

	err := data.Encode(ctx, buf)
	if err != nil {
		return err
	}
	return db.Put(key, buf.Bytes())
}

// @auto-generate: gob
type OrderOnlyStringsCache map[string][]string

type ModuleBuildActionCacheInput struct {
	PropertiesHash proptools.Hash
	ProvidersHash  [][]proptools.Hash
}

type Incremental interface {
	IncrementalSupported() bool
}

type IncrementalModule struct{}

func (m *IncrementalModule) IncrementalSupported() bool {
	return true
}
