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
	"fmt"
	"slices"
	"strconv"
	"testing"
)

func ExampleUniqueList() {
	a := []string{"a", "b", "c", "d"}
	uniqueA := Make(a)
	b := slices.Clone(a)
	uniqueB := Make(b)
	fmt.Println(uniqueA == uniqueB)
	fmt.Println(uniqueA.ToSlice())

	// Output: true
	// [a b c d]
}

func testSlice(n int) []int {
	var slice []int
	for i := 0; i < n; i++ {
		slice = append(slice, i)
	}
	return slice
}

func TestUniqueList(t *testing.T) {
	testCases := []struct {
		name string
		in   []int
	}{
		{
			name: "nil",
			in:   nil,
		},
		{
			name: "zero",
			in:   []int{},
		},
		{
			name: "one",
			in:   testSlice(1),
		},
		{
			name: "nodeSize_minus_one",
			in:   testSlice(nodeSize - 1),
		},
		{
			name: "nodeSize",
			in:   testSlice(nodeSize),
		},
		{
			name: "nodeSize_plus_one",
			in:   testSlice(nodeSize + 1),
		},
		{
			name: "two_times_nodeSize_minus_one",
			in:   testSlice(2*nodeSize - 1),
		},
		{
			name: "two_times_nodeSize",
			in:   testSlice(2 * nodeSize),
		},
		{
			name: "two_times_nodeSize_plus_one",
			in:   testSlice(2*nodeSize + 1),
		},
		{
			name: "large",
			in:   testSlice(1000),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			uniqueList := Make(testCase.in)

			if g, w := uniqueList.ToSlice(), testCase.in; !slices.Equal(g, w) {
				t.Errorf("incorrect ToSlice()\nwant: %q\ngot:  %q", w, g)
			}

			if g, w := slices.Collect(uniqueList.Iter()), testCase.in; !slices.Equal(g, w) {
				t.Errorf("incorrect Iter()\nwant: %q\ngot:  %q", w, g)
			}

			if g, w := uniqueList.AppendTo([]int{-1}), append([]int{-1}, testCase.in...); !slices.Equal(g, w) {
				t.Errorf("incorrect Iter()\nwant: %q\ngot:  %q", w, g)
			}

			if g, w := uniqueList.Len(), len(testCase.in); g != w {
				t.Errorf("incorrect Len(), want %v, got %v", w, g)
			}
		})
	}
}

func BenchmarkMake(b *testing.B) {
	b.Run("unique_ints", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[int], b.N)
		for i := range b.N {
			results[i] = Make([]int{i})
		}
	})
	b.Run("same_ints", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[int], b.N)
		for i := range b.N {
			results[i] = Make([]int{0})
		}
	})
	b.Run("unique_1000_ints", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[int], b.N)
		for i := range b.N {
			b.StopTimer()
			l := make([]int, 1000)
			for j := range 1000 {
				l[j] = i*1000 + j
			}
			b.StartTimer()
			results[i] = Make(l)
		}
	})
	b.Run("same_1000_ints", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[int], b.N)
		l := make([]int, 1000)
		for i := range b.N {
			results[i] = Make(l)
		}
	})
	b.Run("unique_strings", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[string], b.N)
		for i := 0; i < b.N; i++ {
			results[i] = Make([]string{strconv.Itoa(i)})
		}
	})
	b.Run("same_strings", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[string], b.N)
		for i := 0; i < b.N; i++ {
			results[i] = Make([]string{"foo"})
		}
	})
	b.Run("unique_1000_strings", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[string], b.N)
		for i := range b.N {
			b.StopTimer()
			l := make([]string, 1000)
			for j := range 1000 {
				l[j] = strconv.Itoa(i*1000 + j)
			}
			b.StartTimer()
			results[i] = Make(l)
		}
	})
	b.Run("same_1000_strings", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]UniqueList[string], b.N)
		l := slices.Repeat([]string{"foo"}, 1000)
		for i := range b.N {
			results[i] = Make(l)
		}
	})
}

func BenchmarkToSlice(b *testing.B) {
	b.Run("one", func(b *testing.B) {
		b.ReportAllocs()
		handle := Make([]int{1})
		for _ = range b.N {
			handle.ToSlice()
		}
	})
	b.Run("1000", func(b *testing.B) {
		b.ReportAllocs()
		handle := Make(slices.Repeat([]int{1}, 1000))
		for _ = range b.N {
			handle.ToSlice()
		}
	})
}
