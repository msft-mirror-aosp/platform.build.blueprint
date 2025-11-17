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

func (g *gobGen) encodeDepSet(encodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	typeRef := g.findTypeReference(t.Index, pkgName)
	if typeRef.typeName == "string" {
		encodeBody.WriteString(fmt.Sprintf("\tif err = %s.EncodeString(ctx, buf); err != nil { return err }\n", fieldName))
	} else if g.findType(typeRef) == Interface {
		encodeBody.WriteString(fmt.Sprintf("\tif err = %s.EncodeInterface(ctx, buf); err != nil { return err }\n", fieldName))
	} else {
		encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(ctx, buf); err != nil { return err }\n", fieldName))
	}
}

func (g *gobGen) hashDepSet(hashBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
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

func (g *gobGen) decodeDepSet(decodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	typeRef := g.findTypeReference(t.Index, pkgName)
	if typeRef.typeName == "string" {
		decodeBody.WriteString(fmt.Sprintf("\tif err = %s.DecodeString(ctx, buf); err != nil { return err }\n", fieldName))
	} else if g.findType(typeRef) == Interface {
		decodeBody.WriteString(fmt.Sprintf("\tif err = %s.DecodeInterface(ctx, buf); err != nil { return err }\n", fieldName))
	} else {
		decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(ctx, buf); err != nil { return err }\n", fieldName))
	}
}
