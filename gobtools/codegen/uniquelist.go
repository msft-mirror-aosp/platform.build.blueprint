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
	isZeroValue := g.nextVar()
	typeRef := g.findTypeReference(t.Index, pkgName)
	g.maybeAddImport(typeRef)

	// Check if the list is empty/nil
	encodeBody.WriteString(fmt.Sprintf("\t%s := %s.Len() == 0\n", isZeroValue, fieldName))
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeBool(buf, %s); err != nil { return err }\n", isZeroValue))

	encodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isZeroValue))
	// Use EncodeReference to deduplicate the internal pointer of the UniqueList
	encodeBody.WriteString(fmt.Sprintf("\t\tif err = gobtools.EncodeReference(ctx, %s, buf, func(v uniquelist.UniqueList[%s], buf *bytes.Buffer) error {\n", fieldName, typeRef.fullName()))

	// Within the reference callback, encode the underlying slice
	listName := g.nextVar()
	encodeBody.WriteString(fmt.Sprintf("\t\t\t%s := v.ToSlice()\n", listName))
	g.encodeSlice(encodeBody, pkgName, t.Index, listName)

	encodeBody.WriteString(fmt.Sprintf("\t\t\treturn nil\n"))
	encodeBody.WriteString(fmt.Sprintf("\t\t}); err != nil { return err }\n"))
	encodeBody.WriteString(fmt.Sprintf("\t}\n"))

	g.imports[`"github.com/google/blueprint/uniquelist"`] = true
	g.imports[`"github.com/google/blueprint/gobtools"`] = true
}

func (g *gobGen) hashUniqueList(hashBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	typeRef := g.findTypeReference(t.Index, pkgName)
	g.maybeAddImport(typeRef)
	typeName := typeRef.fullName()
	hashFuncBody := &strings.Builder{}
	varName := g.nextVar()
	g.generateHashForType(hashFuncBody, pkgName, t.Index, varName)
	hashFunc := g.nextVar()
	hashBody.WriteString(fmt.Sprintf("\t%s := func(hasher *proptools.Hasher, %s %s) error { %s return nil }\n", hashFunc, varName, typeName, hashFuncBody.String()))
	hashBody.WriteString(fmt.Sprintf("\tif err := %s.Hash(hasher, \"%s\", %s); err != nil { return err }\n", fieldName, typeName, hashFunc))
}

func (g *gobGen) decodeUniqueList(decodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	isZeroValue := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\tvar %s bool\n", isZeroValue))
	decodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.DecodeBool(buf, &%s); err != nil { return err }\n", isZeroValue))

	decodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isZeroValue))
	typeRef := g.findTypeReference(t.Index, pkgName)
	g.maybeAddImport(typeRef)

	// Use DecodeReference to re-use existing UniqueList instances if they were already decoded
	decodeBody.WriteString(fmt.Sprintf("\t\ttmp, err := gobtools.DecodeReference(ctx, &%s, buf, func(value *uniquelist.UniqueList[%s], buf *bytes.Reader) error {\n", fieldName, typeRef.fullName()))

	listName := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\t\t\tvar %s []%s\n", listName, typeRef.fullName()))
	g.decodeSlice(decodeBody, pkgName, t.Index, listName)

	// Reify the slice into the canonical UniqueList
	decodeBody.WriteString(fmt.Sprintf("\t\t\t*value = uniquelist.Make(%s)\n", listName))
	decodeBody.WriteString(fmt.Sprintf("\t\t\treturn nil\n"))
	decodeBody.WriteString(fmt.Sprintf("\t\t})\n"))

	decodeBody.WriteString(fmt.Sprintf("\t\tif err != nil { return err }\n"))
	decodeBody.WriteString(fmt.Sprintf("\t\t%s = *tmp\n", fieldName))
	decodeBody.WriteString(fmt.Sprintf("\t}\n"))

	g.imports[`"github.com/google/blueprint/uniquelist"`] = true
	g.imports[`"github.com/google/blueprint/gobtools"`] = true
}
