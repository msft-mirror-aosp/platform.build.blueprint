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
	"go/types"
)

type typeReference struct {
	prefix     string
	curPackage bool
	pkgName    string
	typeName   string
}

func (t typeReference) fullName() string {
	if t.curPackage {
		return t.prefix + t.typeName
	} else {
		return t.prefix + t.pkgName + "." + t.typeName
	}
}

func (g *gobGen) findType(typeRef typeReference) typeDefTypes {
	if _, ok := g.pkgStructs[typeRef.pkgName]; !ok {
		g.importPackage(typeRef.pkgName, g.findPackagePath(typeRef.pkgName))
	}

	ts := g.pkgStructs[typeRef.pkgName][typeRef.typeName]
	if ts == nil {
		return Unknown
	}

	// 1. Check for Type Alias
	if ts.Assign.IsValid() {
		return Alias
	}

	// 2. If not an alias, inspect ts.Type
	switch t := ts.Type.(type) {
	case *ast.InterfaceType:
		return Interface
	case *ast.StructType:
		return Struct
	case *ast.Ident:
		return Ident
	case *ast.MapType:
		return Map
	case *ast.ArrayType:
		{
			if t.Len == nil {
				return Slice
			} else {
				return Array
			}
		}
	case *ast.StarExpr:
		return Pointer
	}
	return Unknown
}

func (g *gobGen) findTypeReference(expr ast.Expr, pkgName string) typeReference {
	var typeRef typeReference
	switch t := expr.(type) {
	case *ast.Ident:
		for _, basicType := range types.Typ {
			if t.Name == basicType.Name() {
				// basic types (like int) have no package
				pkgName = ""
			}
		}
		typeRef.pkgName = pkgName
		typeRef.typeName = t.Name
	case *ast.SelectorExpr:
		typeRef.pkgName = t.X.(*ast.Ident).Name
		typeRef.typeName = t.Sel.Name
	case *ast.ArrayType:
		typeRef = g.findTypeReference(t.Elt, pkgName)
		if t.Len == nil {
			// slice
			typeRef.prefix = "[]" + typeRef.prefix
		} else {
			// array
			typeRef.prefix = "[" + t.Len.(*ast.BasicLit).Value + "]" + typeRef.prefix
		}
	case *ast.StarExpr:
		typeRef = g.findTypeReference(t.X, pkgName)
		typeRef.prefix = "*" + typeRef.prefix
	case *ast.MapType:
		typeRef = g.findTypeReference(t.Value, pkgName)
		keyTypeRef := g.findTypeReference(t.Key, pkgName)
		typeRef.prefix = "map[" + keyTypeRef.typeName + "]" + typeRef.prefix
	default:
		panic(fmt.Errorf("unknown type to find name: %T", expr))
	}

	typeRef.curPackage = typeRef.pkgName == g.curPackage || typeRef.pkgName == ""

	return typeRef
}
