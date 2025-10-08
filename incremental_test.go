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

package blueprint

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/google/blueprint/proptools"
)

func bpSetup(t *testing.T, bp string) *Context {
	ctx := NewContext()
	fileSystem := map[string][]byte{
		"Android.bp": []byte(bp),
		"file1.cc":   {},
		"file1.cpp":  {},
		"file2.cc":   {},
		"file2.cpp":  {},
	}
	ctx.MockFileSystem(fileSystem)
	ctx.RegisterBottomUpMutator("deps", depsMutator)
	ctx.RegisterModuleType("incremental_module", newIncrementalModule)
	ctx.RegisterModuleType("foo_module", newFooModule)
	ctx.RegisterModuleType("bar_module", newBarModule)

	_, errs := ctx.ParseBlueprintsFiles("Android.bp", nil)
	if len(errs) > 0 {
		t.Errorf("unexpected parse errors:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	_, errs = ctx.ResolveDependencies(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected dep errors:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	return ctx
}

func incrementalSetup(t *testing.T) *Context {
	bp := `
			incremental_module {
					name: "MyIncrementalModule",
					deps: ["MyBarModule"],
					outputs: ["MyIncrementalModule_phony_output"],
					order_only: ["test.lib"],
					srcs: [
							"*.cc",
							"*.cpp",
					],
					exclude_srcs: [
							"file1.cc",
							"file1.cpp",
					],
			}
			bar_module {
					name: "MyBarModule",
					outputs: ["MyBarModule_phony_output"],
					order_only: ["test.lib"],
			}
			foo_module {
					name: "MyFooModule",
					outputs: ["MyFooModule_phony_output"],
					order_only: ["test.lib"],
					deps: ["MyIncrementalModule"],
			}
		`

	ctx := bpSetup(t, bp)

	cache := &BuildActionCache{}
	err := cache.openForTests()
	if err != nil {
		t.Fatalf("failed to open cache: %s", err)
	}
	ctx.buildActionsCache = cache

	return ctx
}

func incrementalSetupForRestore(ctx *Context, orderOnlyStrings []string) any {
	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()

	providerHashes := make([]proptools.Hash, len(providerRegistry))
	// Use fixed value since SetProvider hasn't been called yet, so we can't go
	// through the providers of the module.
	for k, v := range map[providerKey]any{
		IncrementalTestProviderKey.providerKey: IncrementalTestInfo{
			Value: barInfo.Name(),
		},
	} {
		hash, err := proptools.CalculateHash(v)
		if err != nil {
			panic("Can't hash value of providers")
		}
		providerHashes[k.id] = hash
	}
	cacheKey, hash := calculateHashKey(incInfo, [][]proptools.Hash{providerHashes})
	var providerValue any = IncrementalTestInfo{Value: "MyIncrementalModule"}
	providerHash, _ := proptools.CalculateHash(providerValue)
	ctx.buildActionsCache.writeModuleBuildAction(ctx.EncContext, &cacheKey, &ModuleActionCachedData{
		InputHash: hash,
		ProviderHashes: []ProviderHash{{
			Id:   &IncrementalTestProviderKey.providerKey,
			Hash: providerHash,
		}},
		OrderOnlyStrings: orderOnlyStrings,
		GlobCache:        calculateGlobCache(),
	})
	ctx.buildActionsCache.writeProvider(ctx.EncContext, providerHash, CachedProvider{
		Id:    &IncrementalTestProviderKey.providerKey,
		Value: providerValue,
	})
	ctx.buildActionsCache.writeNinjaStatements(&cacheKey, []byte(incrementalModuleNinja))

	ctx.buildActionsCache.flush()

	ctx.SetIncrementalEnabled(true)
	ctx.SetIncrementalAnalysis(true)

	return providerValue
}

func calculateHashKey(m *moduleInfo, providerHashes [][]proptools.Hash) (BuildActionCacheKey, proptools.Hash) {
	hash, err := proptools.CalculateHash(m.properties)
	if err != nil {
		panic(newPanicErrorf(err, "failed to calculate properties hash"))
	}
	cacheInput := new(ModuleBuildActionCacheInput)
	cacheInput.PropertiesHash = hash
	cacheInput.ProvidersHash = providerHashes
	hash, err = proptools.CalculateHash(cacheInput)
	if err != nil {
		panic(newPanicErrorf(err, "failed to calculate cache input hash"))
	}
	return BuildActionCacheKey{
		Id: m.ModuleCacheKey(),
	}, hash
}

func calculateGlobCache() []globResultCache {
	globHash1, _ := proptools.CalculateHash([]string{"file2.cc"})
	globHash2, _ := proptools.CalculateHash([]string{"file2.cpp"})

	return []globResultCache{
		{
			Pattern:  "*.cc",
			Excludes: []string{"file1.cc", "file1.cpp"},
			Result:   globHash1,
		},
		{
			Pattern:  "*.cpp",
			Excludes: []string{"file1.cc", "file1.cpp"},
			Result:   globHash2,
		},
	}
}

func TestCacheBuildActions(t *testing.T) {
	ctx := incrementalSetup(t)
	ctx.SetIncrementalEnabled(true)

	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")

	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()

	ctx.buildActionsCache.flush()

	cacheKey, hash := calculateHashKey(incInfo, [][]proptools.Hash{barInfo.providerInitialValueHashes})
	cache, err := ctx.buildActionsCache.readModuleBuildAction(ctx.EncContext, &cacheKey)
	if err != nil {
		t.Fatalf("read failed with an error: %s", err)
	}
	if cache == nil {
		t.Errorf("failed to find cached build actions for the incremental module")
	}
	var providerValue any = IncrementalTestInfo{Value: "MyIncrementalModule"}
	providerHash, _ := proptools.CalculateHash(providerValue)
	expectedCache := ModuleActionCachedData{
		InputHash: hash,
		ProviderHashes: []ProviderHash{{
			Id:   &IncrementalTestProviderKey.providerKey,
			Hash: providerHash,
		}},
		OrderOnlyStrings: []string{"dedup-d479e9a8133ff998"},
		GlobCache:        calculateGlobCache(),
	}
	if !reflect.DeepEqual(expectedCache, *cache) {
		t.Errorf("expected: %v actual %v", expectedCache, *cache)
	}

	provider, err := ctx.buildActionsCache.readProvider(ctx.EncContext, providerHash, &IncrementalTestProviderKey.providerKey)
	if err != nil {
		t.Fatalf("read failed with an error: %s", err)
	}
	if *provider.Id != IncrementalTestProviderKey.providerKey {
		t.Errorf("expected restored id: %v actual %v", IncrementalTestProviderKey.providerKey, *provider.Id)
	}
	if !reflect.DeepEqual(provider.Value, providerValue) {
		t.Errorf("expected: %v actual %v", providerValue, provider.Value)
	}

	ninja, err := ctx.buildActionsCache.readNinjaStatements(&cacheKey)
	if err != nil {
		t.Fatalf("read failed with an error: %s", err)
	}
	ninjaStr := string(ninja)
	if !strings.Contains(ninjaStr, incrementalModuleNinja) {
		t.Errorf("expected: %v actual %v", incrementalModuleNinja, ninjaStr)
	}
}

func TestRestoreBuildActions(t *testing.T) {
	ctx := incrementalSetup(t)
	providerValue := incrementalSetupForRestore(ctx, nil)
	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	// Verify that the GenerateBuildActions was skipped for the incremental module
	incRerun := incInfo.logicModule.(*incrementalModule).GenerateBuildActionsCalled
	barRerun := barInfo.logicModule.(*barModule).GenerateBuildActionsCalled
	if incRerun || !barRerun {
		t.Errorf("failed to skip/rerun GenerateBuildActions: %t %t", incRerun, barRerun)
	}
	// Verify that the provider is set correctly for the incremental module
	if !reflect.DeepEqual(incInfo.providers[IncrementalTestProviderKey.id], providerValue) {
		t.Errorf("provider is not set correctly when restoring from cache")
	}
}

func TestGlobChangeRestoreBuildActions(t *testing.T) {
	ctx := incrementalSetup(t)
	incrementalSetupForRestore(ctx, nil)
	// Now change the file system to make the old glob result invalid.
	fileSystem := map[string][]byte{
		"Android.bp": {},
		"file1.cc":   {},
		"file1.cpp":  {},
		"file3.cc":   {},
		"file4.cpp":  {},
	}
	ctx.MockFileSystem(fileSystem)
	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	// Verify that the GenerateBuildActions was rerun for the incremental module
	incRerun := incInfo.logicModule.(*incrementalModule).GenerateBuildActionsCalled
	if !incRerun {
		t.Errorf("failed to rerun GenerateBuildActions when glob result changed: %t", incRerun)
	}
}

func TestSkipNinjaForCacheHit(t *testing.T) {
	ctx := incrementalSetup(t)
	incrementalSetupForRestore(ctx, nil)
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")
	// Verify that soong updated the ninja file for the bar module and skipped the
	// ninja file writing of the incremental module
	file, err := ctx.fs.Open("test.0.ninja")
	if err != nil {
		t.Errorf("no ninja file for MyBarModule")
	}
	content := make([]byte, 1024)
	file.Read(content)
	if !strings.Contains(string(content), "build MyBarModule_phony_output: phony") {
		t.Errorf("ninja file doesn't have build statements for MyBarModule: %s", string(content))
	}

	file, err = ctx.fs.Open("test.2.ninja")
	if err != nil {
		t.Errorf("no ninja file for MyIncrementalModule")
	}
	content = make([]byte, 1024)
	file.Read(content)
	if !strings.Contains(string(content), incrementalModuleNinja) {
		t.Errorf("ninja file doesn't have build statements for MyIncrementalModule: %s", string(content))
	}
}

func TestNotSkipNinjaForCacheMiss(t *testing.T) {
	ctx := incrementalSetup(t)
	ctx.SetIncrementalEnabled(true)
	ctx.SetIncrementalAnalysis(true)
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")
	// Verify that soong updated the ninja files for both the bar module and the
	// incremental module
	file, err := ctx.fs.Open("test.0.ninja")
	if err != nil {
		t.Errorf("no ninja file for MyBarModule")
	}
	content := make([]byte, 1024)
	file.Read(content)
	if !strings.Contains(string(content), "build MyBarModule_phony_output: phony") {
		t.Errorf("ninja file doesn't have build statements for MyBarModule: %s", string(content))
	}

	file, err = ctx.fs.Open("test.2.ninja")
	if err != nil {
		t.Errorf("no ninja file for MyIncrementalModule")
	}
	content = make([]byte, 1024)
	file.Read(content)
	if !strings.Contains(string(content), "build MyIncrementalModule_phony_output: phony") {
		t.Errorf("ninja file doesn't have build statements for MyIncrementalModule: %s", string(content))
	}
}

func TestOrderOnlyStringsCaching(t *testing.T) {
	phony := "dedup-d479e9a8133ff998"
	ctx := incrementalSetup(t)
	ctx.SetIncrementalEnabled(true)
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}
	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")

	ctx.buildActionsCache.flush()

	verifyOrderOnlyStringsCache(t, ctx, incInfo, barInfo)

	// Verify dedup-d479e9a8133ff998 is written to the common ninja file.
	expected := strings.Join([]string{"build", phony + ":", "phony", "test.lib"}, " ")
	if strings.Count(buf.String(), expected) != 1 {
		t.Errorf("only one phony target should be found: %s", buf.String())
	}
}

func TestOrderOnlyStringsRestoring(t *testing.T) {
	phony := "dedup-d479e9a8133ff998"
	orderOnlyStrings := []string{phony}
	ctx := incrementalSetup(t)
	incrementalSetupForRestore(ctx, orderOnlyStrings)
	ctx.orderOnlyStringsCache = make(OrderOnlyStringsCache)
	ctx.orderOnlyStringsCache[phony] = []string{"test.lib"}
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")

	ctx.buildActionsCache.flush()

	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	verifyOrderOnlyStringsCache(t, ctx, incInfo, barInfo)

	verifyBuildDefsShouldContain(t, barInfo, phony)
	// Verify dedup-d479e9a8133ff998 is written to the common ninja file.
	expected := strings.Join([]string{"build", phony + ":", "phony", "test.lib"}, " ")
	if strings.Count(buf.String(), expected) != 1 {
		t.Errorf("only one phony target should be found: %s", buf.String())
	}

	if len(ctx.orderOnlyStringsCache) != 1 {
		t.Errorf("Phony target should be cached: %s", buf.String())
	}
}

func TestOrderOnlyStringsValidWhenOnlyRestoredModuleUseIt(t *testing.T) {
	phony := "dedup-d479e9a8133ff998"
	orderOnlyStrings := []string{phony}
	bp := `
			incremental_module {
					name: "MyIncrementalModule",
					deps: ["MyBarModule"],
					outputs: ["MyIncrementalModule_phony_output"],
					order_only: ["test.lib"],
			}
			bar_module {
					name: "MyBarModule",
					outputs: ["MyBarModule_phony_output"],
			}
		`

	ctx := bpSetup(t, bp)
	cache := &BuildActionCache{}
	err := cache.openForTests()
	if err != nil {
		t.Fatalf("failed to open cache: %s", err)
	}
	ctx.buildActionsCache = cache
	incrementalSetupForRestore(ctx, orderOnlyStrings)
	ctx.orderOnlyStringsCache = make(OrderOnlyStringsCache)
	ctx.orderOnlyStringsCache[phony] = []string{"test.lib"}
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")

	ctx.buildActionsCache.flush()

	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	verifyOrderOnlyStringsCache(t, ctx, incInfo, barInfo)

	// Verify dedup-d479e9a8133ff998 is still written to the common ninja file even
	// though MyBarModule no longer uses it.
	expected := strings.Join([]string{"build", phony + ":", "phony", "test.lib"}, " ")
	if strings.Count(buf.String(), expected) != 1 {
		t.Errorf("only one phony target should be found: %s", buf.String())
	}

	if len(ctx.orderOnlyStringsCache) != 1 {
		t.Errorf("Phony target should be cached: %s", buf.String())
	}
}

func TestCachedModuleRemoved(t *testing.T) {
	phony := "dedup-d479e9a8133ff998"
	orderOnlyStrings := []string{phony}
	ctx := incrementalSetup(t)
	incrementalSetupForRestore(ctx, orderOnlyStrings)
	bp := `
			bar_module {
					name: "MyBarModule",
					outputs: ["MyBarModule_phony_output"],
					order_only: ["test.lib"],
			}
		`
	ctx = bpSetup(t, bp)
	ctx.orderOnlyStringsCache = make(OrderOnlyStringsCache)
	ctx.orderOnlyStringsCache[phony] = []string{"test.lib"}
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")

	// Verify dedup-d479e9a8133ff998 is no longer written to the common ninja file
	// because MyIncrementalModule was removed so only MyBarModule still use it.
	expected := strings.Join([]string{"build", phony + ":", "phony", "test.lib"}, " ")
	if strings.Count(buf.String(), expected) != 0 {
		t.Errorf("Phony target should not be present in ninja file: %s", buf.String())
	}
	if len(ctx.orderOnlyStringsCache) != 0 {
		t.Errorf("Phony target should not be cached: %s", buf.String())
	}
}

// This tests the scenario where one restored module and two non-restored modules
// share the same set of order only strings. The two non-restored modules will
// contribute a dedup phony target in this case, and the restored module shouldn't
// add a duplicate one.
func TestSharedOrderOnlyStringsRestoringNoDuplicates(t *testing.T) {
	phony := "dedup-d479e9a8133ff998"
	orderOnlyStrings := []string{phony}
	ctx := incrementalSetup(t)
	incrementalSetupForRestore(ctx, orderOnlyStrings)
	ctx.orderOnlyStringsCache = make(OrderOnlyStringsCache)
	ctx.orderOnlyStringsCache[phony] = []string{"test.lib"}

	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Errorf("unexpected errors calling generateModuleBuildActions:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}
	incInfo := ctx.moduleGroupFromName("MyIncrementalModule", nil).modules.firstModule()
	fooInfo := ctx.moduleGroupFromName("MyFooModule", nil).modules.firstModule()
	barInfo := ctx.moduleGroupFromName("MyBarModule", nil).modules.firstModule()

	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	ctx.writeAllModuleActions(w, true, "test.ninja")

	ctx.buildActionsCache.flush()

	verifyOrderOnlyStringsCache(t, ctx, incInfo, barInfo)
	verifyBuildDefsShouldContain(t, fooInfo, phony)
	verifyBuildDefsShouldContain(t, barInfo, phony)

	// Verify dedup-d479e9a8133ff998 is written to the common ninja file.
	expected := strings.Join([]string{"build", phony + ":", "phony", "test.lib"}, " ")
	if strings.Count(buf.String(), expected) != 1 {
		t.Errorf("only one phony target should be found: %s", buf.String())
	}

	if len(ctx.orderOnlyStringsCache) != 1 {
		t.Errorf("Phony target should be cached: %s", buf.String())
	}
}

func verifyBuildDefsShouldContain(t *testing.T, module *moduleInfo, expected string) {
	found := false
	for _, def := range module.actionDefs.buildDefs {
		found = listContainsValue(def.OrderOnlyStrings.ToSlice(), expected)
		if found {
			break
		}
	}
	if !found {
		t.Errorf("%s should have dedup phony target: %v", module.Name(), module.actionDefs.buildDefs)
	}
}

func verifyOrderOnlyStringsCache(t *testing.T, ctx *Context, incInfo, barInfo *moduleInfo) {
	// Verify that soong cache all the order only strings that are used by the
	// incremental modules
	ok, key := mapContainsValue(ctx.orderOnlyStringsCache, "test.lib")
	if !ok {
		t.Errorf("no order only strings used by incremetnal modules cached: %v", ctx.orderOnlyStringsCache)
	}

	// Verify that the dedup-* order only strings used by MyIncrementalModule is
	// cached along with its other cached values
	cacheKey, _ := calculateHashKey(incInfo, [][]proptools.Hash{barInfo.providerInitialValueHashes})
	cache, err := ctx.buildActionsCache.readModuleBuildAction(ctx.EncContext, &cacheKey)
	if err != nil {
		t.Fatalf("read failed with an error: %s", err)
	}
	if cache == nil {
		t.Errorf("failed to find cached build actions for the incremental module")
	}
	if !listContainsValue(cache.OrderOnlyStrings, key) {
		t.Errorf("no order only strings cached for MyIncrementalModule: %v", cache.OrderOnlyStrings)
	}
}

func listContainsValue[K comparable](l []K, target K) bool {
	for _, value := range l {
		if value == target {
			return true
		}
	}
	return false
}

func mapContainsValue[K comparable, V comparable](m map[K][]V, target V) (bool, K) {
	for k, v := range m {
		if listContainsValue(v, target) {
			return true, k
		}
	}
	var key K
	return false, key
}

var singletonTestInfoProvider = NewSingletonProvider[IncrementalTestInfo]()

type sequentialSingleton struct {
	GenerateBuildActionsCalled int
}

func (s *sequentialSingleton) GenerateBuildActions(ctx SingletonContext) {
	s.GenerateBuildActionsCalled++
	ctx.Build(pctx, BuildParams{
		Rule:    Phony,
		Outputs: []string{sequentialSingletonName},
	})
	ctx.VisitAllSingletons(func(singleton SingletonProxy) {
		ctx.OtherSingletonProvider(singleton, singletonTestInfoProvider)
	})
	ctx.VisitAllModules(func(module Module) {
		ctx.ModuleProvider(module, IncrementalTestProviderKey)
	})
	ctx.SetSingletonProvider(singletonTestInfoProvider, IncrementalTestInfo{Value: sequentialSingletonName})
}

func (s *sequentialSingleton) IncrementalSupported() bool {
	return true
}

func sequentialSingletonFactory() Singleton {
	return &sequentialSingleton{}
}

type parallelSingleton struct{}

func (s *parallelSingleton) GenerateBuildActions(ctx SingletonContext) {
	ctx.SetSingletonProvider(singletonTestInfoProvider, IncrementalTestInfo{Value: parallelSingletonName})
}

func parallelSingletonFactory() Singleton {
	return &parallelSingleton{}
}

func (s *parallelSingleton) IncrementalSupported() bool {
	return true
}

var parallelSingletonName = "parallel_singleton"

const sequentialSingletonName = "sequential_singleton"

func singletonCacheSetup(t *testing.T) *Context {
	bp := `
			foo_module {
					name: "MyFooModule",
					outputs: ["MyFooModule_phony_output"],
			}
		`
	ctx := bpSetup(t, bp)
	ctx.RegisterSingletonType(parallelSingletonName, parallelSingletonFactory, true)
	ctx.RegisterSingletonType(sequentialSingletonName, sequentialSingletonFactory, false)

	cache := &BuildActionCache{}
	if err := cache.openForTests(); err != nil {
		t.Fatalf("failed to open cache: %s", err)
	}
	ctx.buildActionsCache = cache

	ctx.SetIncrementalEnabled(true)
	ctx.SetIncrementalAnalysis(true)
	return ctx
}

func TestSingletonCache(t *testing.T) {
	ctx := singletonCacheSetup(t)

	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	seqSingleton := ctx.singletonByName(sequentialSingletonName).singleton.(*sequentialSingleton)

	// 1. Verify GenerateBuildActions was called
	if seqSingleton.GenerateBuildActionsCalled != 1 {
		t.Errorf("expected GenerateBuildActions to be called once, got %d", sequentialSingleton{}.GenerateBuildActionsCalled)
	}

	ctx.buildActionsCache.flush()

	// 2. Verify cache entry was written
	seqCacheKey := &BuildActionCacheKey{Id: sequentialSingletonName}
	data, err := ctx.buildActionsCache.readSingletonBuildAction(ctx.EncContext, seqCacheKey)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}
	if data == nil || len(data.DependencyProviderHashes) != 2 {
		t.Errorf("expected cache entry to be written with 2 provider hashes, got nil or %d hashes", len(data.DependencyProviderHashes))
	}

	// 3. Verify providers were cached
	seqSingletonProviderHash := ctx.singletonByName(sequentialSingletonName).providerInitialValueHashes[singletonTestInfoProvider.providerKey.id]

	provider, err := ctx.buildActionsCache.readProvider(ctx.EncContext, seqSingletonProviderHash, &singletonTestInfoProvider.providerKey)
	if err != nil {
		t.Fatalf("read failed with an error: %s", err)
	}
	if *provider.Id != singletonTestInfoProvider.providerKey {
		t.Errorf("expected restored id: %v actual %v", IncrementalTestProviderKey.providerKey, *provider.Id)
	}
	var providerValue any = IncrementalTestInfo{Value: sequentialSingletonName}
	if !reflect.DeepEqual(provider.Value, providerValue) {
		t.Errorf("expected: %v actual %v", providerValue, provider.Value)
	}

	// 4. Verify ninja statement was cached
	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	if err := ctx.writeAllSingletonActions(w); err != nil {
		t.Fatalf("failed to write all singleton actions: %v", err)
	}
	ninja, err := ctx.buildActionsCache.readNinjaStatements(seqCacheKey)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}
	if !strings.Contains(string(ninja), "build sequential_singleton: phony") {
		t.Errorf("expected ninja statement to be cached")
	}

	// 5. Verify ninja file was written
	if !strings.Contains(buf.String(), "build sequential_singleton: phony") {
		t.Errorf("ninja file doesn't have build statements for singleton: %s", buf.String())
	}
}

func TestSingletonRestore(t *testing.T) {
	ctx := singletonCacheSetup(t)
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	if err := ctx.writeAllSingletonActions(w); err != nil {
		t.Fatalf("failed to write all singleton actions: %v", err)
	}

	ctx.buildActionsCache.flush()

	cacheKey := &BuildActionCacheKey{Id: sequentialSingletonName}
	data, err := ctx.buildActionsCache.readSingletonBuildAction(ctx.EncContext, cacheKey)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}
	ninja, err := ctx.buildActionsCache.readNinjaStatements(cacheKey)
	if err != nil {
		t.Fatalf("failed to read ninja statements: %v", err)
	}
	seqSingletonProviderHash := ctx.singletonByName(sequentialSingletonName).providerInitialValueHashes[singletonTestInfoProvider.providerKey.id]
	provider, err := ctx.buildActionsCache.readProvider(ctx.EncContext, seqSingletonProviderHash, &singletonTestInfoProvider.providerKey)
	if err != nil {
		t.Fatalf("failed to read provider: %v", err)
	}

	// Now simulate an incremental build
	ctx = singletonCacheSetup(t)
	ctx.buildActionsCache.writeSingletonBuildAction(ctx.EncContext, cacheKey, data)
	ctx.buildActionsCache.writeNinjaStatements(cacheKey, ninja)
	ctx.buildActionsCache.writeProvider(ctx.EncContext, seqSingletonProviderHash, provider)

	ctx.buildActionsCache.flush()

	_, errs = ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	seqSingletonInfo := ctx.singletonByName(sequentialSingletonName)
	seqSingleton := seqSingletonInfo.singleton.(*sequentialSingleton)

	// 1. Verify GenerateBuildActions was not called
	if seqSingleton.GenerateBuildActionsCalled != 0 {
		t.Errorf("expected GenerateBuildActions to be not called, got %d", seqSingleton.GenerateBuildActionsCalled)
	}

	// 2. Verify that the provider is set correctly for the singleton
	expected := IncrementalTestInfo{Value: sequentialSingletonName}
	actual, found := ctx.singletonProvider(seqSingletonInfo, singletonTestInfoProvider.provider())
	if !found || !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected: %v actual %v", expected, actual)
	}

	// 3. Verify ninja statement was restored
	buf.Reset()
	if err := ctx.writeAllSingletonActions(w); err != nil {
		t.Fatalf("failed to write all singleton actions: %v", err)
	}
	if !strings.Contains(buf.String(), "build sequential_singleton: phony") {
		t.Errorf("ninja file doesn't have build statements for singleton: %s", buf.String())
	}
}

func TestSingletonNotRestoreForSingletonChange(t *testing.T) {
	ctx := singletonCacheSetup(t)
	_, errs := ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	buf := bytes.NewBuffer(nil)
	w := newNinjaWriter(buf)
	if err := ctx.writeAllSingletonActions(w); err != nil {
		t.Fatalf("failed to write all singleton actions: %v", err)
	}

	ctx.buildActionsCache.flush()

	cacheKey := &BuildActionCacheKey{Id: sequentialSingletonName}
	data, err := ctx.buildActionsCache.readSingletonBuildAction(ctx.EncContext, cacheKey)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}
	ninja, err := ctx.buildActionsCache.readNinjaStatements(cacheKey)
	if err != nil {
		t.Fatalf("failed to read ninja statements: %v", err)
	}
	seqSingletonProviderHash := ctx.singletonByName(sequentialSingletonName).providerInitialValueHashes[singletonTestInfoProvider.providerKey.id]
	provider, err := ctx.buildActionsCache.readProvider(ctx.EncContext, seqSingletonProviderHash, &singletonTestInfoProvider.providerKey)
	if err != nil {
		t.Fatalf("failed to read provider: %v", err)
	}

	// Now simulate an incremental build
	parallelSingletonName = parallelSingletonName + "_New"
	ctx = singletonCacheSetup(t)
	ctx.buildActionsCache.writeSingletonBuildAction(ctx.EncContext, cacheKey, data)
	ctx.buildActionsCache.writeNinjaStatements(cacheKey, ninja)
	ctx.buildActionsCache.writeProvider(ctx.EncContext, seqSingletonProviderHash, provider)

	ctx.buildActionsCache.flush()

	_, errs = ctx.PrepareBuildActions(nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	seqSingletonInfo := ctx.singletonByName(sequentialSingletonName)
	seqSingleton := seqSingletonInfo.singleton.(*sequentialSingleton)

	// 1. Verify GenerateBuildActions was called
	if seqSingleton.GenerateBuildActionsCalled != 1 {
		t.Errorf("expected GenerateBuildActions to be called, got %d", seqSingleton.GenerateBuildActionsCalled)
	}
}
