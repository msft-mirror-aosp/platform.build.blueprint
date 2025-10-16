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
	"go/token"
	"strings"
)

func findStructs(node *ast.File) []*ast.TypeSpec {
	var ret []*ast.TypeSpec
	for _, decl := range node.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
			for _, spec := range genDecl.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					if genDecl.Doc != nil && strings.Contains(genDecl.Doc.Text(), genGobAnnotation) {
						ret = append(ret, typeSpec)
					}
				}
			}
		}
	}
	return ret
}

func (g *gobGen) loadTypes(node *ast.File) {
	pkgName := node.Name.Name
	if _, ok := g.pkgStructs[pkgName]; !ok {
		g.pkgStructs[pkgName] = make(map[string]*ast.TypeSpec)
	}
	for _, decl := range node.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
			for _, spec := range genDecl.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					g.pkgStructs[pkgName][typeSpec.Name.Name] = typeSpec
				}
			}
		}
	}
}

func (g *gobGen) maybeAddImport(typeRef typeReference) {
	if typeRef.pkgName != "" && typeRef.pkgName != g.curPackage {
		g.imports[fmt.Sprintf("\"%s\"", g.importPkgs[typeRef.pkgName])] = true
	}
}

func (g *gobGen) nextVar() string {
	g.fieldId++
	return fmt.Sprintf("val%d", g.fieldId)
}
