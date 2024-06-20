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

type BuildActionCacheKey struct {
	Id        string
	InputHash uint64
}

type CachedBuildParams struct {
	Comment         string
	Depfile         string
	Deps            Deps
	Description     string
	Rule            string
	Outputs         []string
	ImplicitOutputs []string
	Inputs          []string
	Implicits       []string
	OrderOnly       []string
	Validations     []string
	Args            map[string]string
	Optional        bool
}

type CachedBuildActions struct {
	BuildParams []CachedBuildParams
}

type CachedProvider struct {
	Id    *providerKey
	Value *any
}

type BuildActionCachedData struct {
	BuildActions CachedBuildActions
	Providers    []CachedProvider
}

type BuildActionCache = map[BuildActionCacheKey]*BuildActionCachedData

type BuildActionCacheInput struct {
	PropertiesHash uint64
	ProvidersHash  [][]uint64
}

type Incremental interface {
	IncrementalSupported() bool
	BuildActionProviderKeys() []AnyProviderKey
	PackageContextPath() string
	CachedRules() []Rule
}

type IncrementalModule struct{}

func (m *IncrementalModule) IncrementalSupported() bool {
	return true
}
