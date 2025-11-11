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

package main

import (
	"fmt"
	"go/ast"
	"strings"
)

func (g *gobGen) generateEncodeForStruct(encodeBody *strings.Builder, pkgName string, t *ast.StructType, fieldName string) {
	for _, f := range t.Fields.List {
		encodeBody.WriteString("\n")
		fName := fieldName + "."
		if len(f.Names) == 0 {
			typeRef := g.findTypeReference(f.Type, pkgName)
			g.generateEncodeForType(encodeBody, pkgName, f.Type, fName+typeRef.typeName)
		} else {
			for _, name := range f.Names {
				g.generateEncodeForType(encodeBody, pkgName, f.Type, fName+name.Name)
			}
		}
	}
}

func (g *gobGen) generateDecodeForStruct(decodeBody *strings.Builder, pkgName string, t *ast.StructType, fieldName string) {
	for _, f := range t.Fields.List {
		decodeBody.WriteString("\n")
		fName := fieldName + "."
		if len(f.Names) == 0 {
			typeRef := g.findTypeReference(f.Type, pkgName)
			g.generateDecodeForType(decodeBody, pkgName, f.Type, fName+typeRef.typeName)
		} else {
			for _, name := range f.Names {
				g.generateDecodeForType(decodeBody, pkgName, f.Type, fName+name.Name)
			}
		}
	}
}

func (g *gobGen) generateHashForStruct(hashBody *strings.Builder, pkgName, structName string, t *ast.StructType, fieldName string) {
	if structName == "" {
		// anonymous struct
		structName = fieldName
	}
	typeNameForHash := g.getTypeNameForHash(pkgName, structName)
	hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", typeNameForHash))
	numFields := 0
	for _, f := range t.Fields.List {
		if len(f.Names) > 0 {
			numFields += len(f.Names)
		} else {
			numFields++ // Count embedded field
		}
	}
	hashBody.WriteString(fmt.Sprintf("\thasher.WriteInt(%d)\n", numFields))

	for _, f := range t.Fields.List {
		fName := fieldName + "."
		if len(f.Names) == 0 {
			typeRef := g.findTypeReference(f.Type, pkgName)
			g.generateHashForType(hashBody, pkgName, f.Type, fName+typeRef.typeName)
		} else {
			for _, name := range f.Names {
				g.generateHashForType(hashBody, pkgName, f.Type, fName+name.Name)
			}
		}
	}
}
