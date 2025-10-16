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

func (g *gobGen) encodeUniqueList(encodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	listName := g.nextVar()
	encodeBody.WriteString(fmt.Sprintf("\t%s := %s.ToSlice()\n", listName, fieldName))
	g.encodeSlice(encodeBody, pkgName, t.Index, listName)
}

func (g *gobGen) decodeUniqueList(decodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	listName := g.nextVar()
	typeRef := g.findTypeReference(t.Index, pkgName)
	decodeBody.WriteString(fmt.Sprintf("\tvar %s []%s\n", listName, typeRef.fullName()))
	g.maybeAddImport(typeRef)
	g.decodeSlice(decodeBody, pkgName, t.Index, listName)
	decodeBody.WriteString(fmt.Sprintf("\t%s = uniquelist.Make(%s)\n", fieldName, listName))
	g.imports[`"github.com/google/blueprint/uniquelist"`] = true
}
