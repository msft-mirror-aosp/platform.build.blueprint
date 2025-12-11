// Copyright 2015 Google Inc. All rights reserved.
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
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/blueprint/pathtools"
)

func verifyGlob(key globKey, pattern string, excludes []string, g pathtools.GlobResult) {
	if pattern != g.Pattern {
		panic(fmt.Errorf("Mismatched patterns %q and %q for glob key %q", pattern, g.Pattern, key))
	}
	if len(excludes) != len(g.Excludes) {
		panic(fmt.Errorf("Mismatched excludes %v and %v for glob key %q", excludes, g.Excludes, key))
	}

	for i := range excludes {
		if g.Excludes[i] != excludes[i] {
			panic(fmt.Errorf("Mismatched excludes %v and %v for glob key %q", excludes, g.Excludes, key))
		}
	}
}

func (c *Context) glob(pattern string, excludes []string) ([]string, error) {
	// Sort excludes so that two globs with the same excludes in a different order reuse the same
	// key.  Make a copy first to avoid modifying the caller's version.
	excludes = slices.Clone(excludes)
	sort.Strings(excludes)

	key := globToKey(pattern, excludes)

	// Try to get existing glob from the stored results
	c.globLock.Lock()
	g, exists := c.globs[key]
	c.globLock.Unlock()

	if exists {
		// Glob has already been done, double check it is identical
		verifyGlob(key, pattern, excludes, g)
		// Return a copy so that modifications don't affect the cached value.
		return slices.Clone(g.Matches), nil
	}

	// Check if the glob was restored from the cache
	var result pathtools.GlobResult
	if cachedGlob, exists := c.restoredGlobsFromCache[key]; exists {
		result = cachedGlob
	} else {
		// Get a globbed file list
		g, err := c.fs.Glob(pattern, excludes, pathtools.FollowSymlinks)
		if err != nil {
			return nil, err
		}
		result = g
	}

	// Store the results
	c.globLock.Lock()
	if g, exists = c.globs[key]; !exists {
		c.globs[key] = result
	}
	c.globLock.Unlock()

	if exists {
		// Getting the list raced with another goroutine, throw away the results and use theirs
		verifyGlob(key, pattern, excludes, g)
		// Return a copy so that modifications don't affect the cached value.
		return slices.Clone(g.Matches), nil
	}

	// Return a copy so that modifications don't affect the cached value.
	return slices.Clone(result.Matches), nil
}

// WriteGlobFile writes the list of globs and a timestamp to files next to finalOutFile.
func (c *Context) WriteGlobFile(finalOutFile string, startTime time.Time) error {
	globKeys := slices.Collect(maps.Keys(c.globs))
	slices.SortFunc(globKeys, func(a, b globKey) int {
		return cmp.Or(cmp.Compare(a.pattern, b.pattern), cmp.Compare(a.excludes, b.excludes))
	})
	globs := make([]pathtools.GlobResult, 0, len(globKeys))
	for _, key := range globKeys {
		globs = append(globs, c.globs[key])
	}

	globsFile, err := os.Create(finalOutFile + ".globs")
	if err != nil {
		return err
	}
	defer globsFile.Close()
	if err := pathtools.WriteGlobFile(globsFile, globs); err != nil {
		return err
	}

	return os.WriteFile(
		finalOutFile+".globs_time",
		[]byte(fmt.Sprintf("%d\n", startTime.UnixMicro())),
		0666,
	)
}

// RestoreGlobsFromCache reads the list of globs that was written by WriteGlobFile during a
// previous run and returns any whose dependencies have not changed to avoid redoing all the
// glob traversals during incremental analysis.
func (c *Context) RestoreGlobsFromCache(finalOutFile string) error {
	c.EventHandler.Begin("restore_globs")
	defer c.EventHandler.End("restore_globs")

	globsFile, err := os.Open(finalOutFile + ".globs")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer globsFile.Close()

	globsTimeBytes, err := os.ReadFile(finalOutFile + ".globs_time")
	if errors.Is(err, os.ErrNotExist) {
		globsTimeBytes = []byte("0")
	} else if err != nil {
		return err
	}

	globsTimeMicros, err := strconv.ParseInt(strings.TrimSpace(string(globsTimeBytes)), 10, 64)
	if err != nil {
		return err
	}

	globs, globMetrics, err := pathtools.RestoreGlobsFromCache(c.fs, globsFile, globsTimeMicros)
	if err != nil {
		return err
	}

	c.restoredGlobMetrics = globMetrics

	c.restoredGlobsFromCache = make(map[globKey]pathtools.GlobResult, len(globs))
	for _, glob := range globs {
		c.restoredGlobsFromCache[globToKey(glob.Pattern, glob.Excludes)] = glob
	}

	return nil
}

// globKey combines a pattern and a list of excludes into a hashable struct to be used as a key in
// a map.
type globKey struct {
	pattern  string
	excludes string
}

// globToKey converts a pattern and an excludes list into a globKey struct that is hashable and
// usable as a key in a map.
func globToKey(pattern string, excludes []string) globKey {
	return globKey{pattern, strings.Join(excludes, "|")}
}
