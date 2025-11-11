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

func (g *gobGen) encodePointer(encodeBody *strings.Builder, pkgName string, t *ast.StarExpr, fieldName string) {
	isNil := g.nextVar()
	encodeBody.WriteString(fmt.Sprintf("\t%s := %s == nil\n", isNil, fieldName))
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeBool(buf, %s); err != nil { return err }\n", isNil))
	encodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isNil))
	g.generateEncodeForType(encodeBody, pkgName, t.X, "(*"+fieldName+")")
	encodeBody.WriteString("\t}\n")
}

func (g *gobGen) decodePointer(decodeBody *strings.Builder, pkgName string, t *ast.StarExpr, valId string, fieldName string) {
	isNil := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\tvar %s bool\n", isNil))
	decodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.DecodeBool(buf, &%s); err != nil { return err }\n", isNil))
	decodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isNil))
	typeRef := g.findTypeReference(t.X, pkgName)
	decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", valId, typeRef.fullName()))
	g.maybeAddImport(typeRef)
	g.generateDecodeForType(decodeBody, pkgName, t.X, valId)
	decodeBody.WriteString(fmt.Sprintf("\t%s = &%s\n", fieldName, valId))
	decodeBody.WriteString("\t}\n")
}

func (g *gobGen) hashPointer(hashBody *strings.Builder, pkgName string, t *ast.StarExpr, fieldName string) {
	isNil := g.nextVar()
	hashBody.WriteString(fmt.Sprintf("\t%s := %s == nil\n", isNil, fieldName))
	hashBody.WriteString(fmt.Sprintf("\tif %s {\n", isNil))
	hashBody.WriteString(fmt.Sprintf("\t\thasher.WriteByte(0)\n")) // 0 for nil
	hashBody.WriteString(fmt.Sprintf("\t} else {\n"))
	hashFuncBody := &strings.Builder{}
	g.generateHashForType(hashFuncBody, pkgName, t.X, "(*"+fieldName+")")
	hashFunc := g.nextVar()
	hashBody.WriteString(fmt.Sprintf("\t%s := func(hasher *proptools.Hasher) error { %s return nil }\n", hashFunc, hashFuncBody.String()))
	hashBody.WriteString(fmt.Sprintf("\tif err := proptools.HashReference(hasher, uintptr(unsafe.Pointer(%s)), %s); err != nil { return err }\n", fieldName, hashFunc))
	hashBody.WriteString(fmt.Sprintf("\t}\n"))
	g.imports[`"unsafe"`] = true
}
