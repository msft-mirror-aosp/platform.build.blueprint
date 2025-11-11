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

// The types that support cmp.Ordered
var orderedMap = map[string]bool{
	"int":     true,
	"int8":    true,
	"int16":   true,
	"int32":   true,
	"int64":   true,
	"uint":    true,
	"uint8":   true,
	"uint16":  true,
	"uint32":  true,
	"uint64":  true,
	"uintptr": true,
	"float32": true,
	"float64": true,
	"string":  true,
}

func (g *gobGen) encodeMap(encodeBody *strings.Builder, pkgName string, t *ast.MapType, fieldName string) {
	g.generateEncodeForNillable(encodeBody, fieldName)
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeInt(buf, len(%s)); err != nil { return err }\n", fieldName))
	k := g.nextVar()
	v := g.nextVar()
	encodeBody.WriteString(fmt.Sprintf("\tfor %s, %s := range %s {\n", k, v, fieldName))
	g.generateEncodeForType(encodeBody, pkgName, t.Key, k)
	g.generateEncodeForType(encodeBody, pkgName, t.Value, v)
	encodeBody.WriteString("\t}\n")
	encodeBody.WriteString("\t}\n")
}

func (g *gobGen) decodeMap(decodeBody *strings.Builder, pkgName string, t *ast.MapType, valId string, fieldName string) {
	kTypeRef := g.findTypeReference(t.Key, pkgName)
	vTypeRef := g.findTypeReference(t.Value, pkgName)
	g.generateDecodeForNillable(decodeBody, valId)
	decodeBody.WriteString(fmt.Sprintf("\t%s = make(map[%s]%s, %s)\n", fieldName, kTypeRef.fullName(), vTypeRef.fullName(), valId))
	g.maybeAddImport(kTypeRef)
	g.maybeAddImport(vTypeRef)
	index := g.nextVar()
	k := g.nextVar()
	v := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < int(%s); %s++ {\n", index, index, valId, index))
	decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", k, kTypeRef.fullName()))
	decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", v, vTypeRef.fullName()))
	g.maybeAddImport(kTypeRef)
	g.maybeAddImport(vTypeRef)
	g.generateDecodeForType(decodeBody, pkgName, t.Key, k)
	g.generateDecodeForType(decodeBody, pkgName, t.Value, v)
	decodeBody.WriteString(fmt.Sprintf("\t%s[%s] = %s\n", fieldName, k, v))
	decodeBody.WriteString("\t}\n")
	decodeBody.WriteString("\t}\n")
}

func (g *gobGen) hashMap(hashBody *strings.Builder, pkgName string, t *ast.MapType, fieldName string) {
	hashBody.WriteString(fmt.Sprintf("\thasher.WriteInt(len(%s))\n", fieldName))

	keysVar := g.nextVar()
	iVar := g.nextVar()
	kVar := g.nextVar()

	// 1. Create a slice of keys
	keyType := g.findTypeReference(t.Key, pkgName).fullName()
	hashBody.WriteString(fmt.Sprintf("\t%s := make([]%s, 0, len(%s))\n", keysVar, g.findTypeReference(t.Key, pkgName).fullName(), fieldName))
	hashBody.WriteString(fmt.Sprintf("\tfor %s := range %s {\n", kVar, fieldName))
	hashBody.WriteString(fmt.Sprintf("\t\t%s = append(%s, %s)\n", keysVar, keysVar, kVar))
	hashBody.WriteString(fmt.Sprintf("\t}\n"))

	// 2. Choose which sorting method to use.
	if _, ok := orderedMap[keyType]; ok {
		hashBody.WriteString(fmt.Sprintf("\tproptools.SortOrdered(%s)\n", keysVar))
	} else {
		// Handle the case such as type TypeBasic int, in which case we need to use SortOrdered.
		typeRef := g.findTypeReference(t.Key, pkgName)
		typ := g.findType(typeRef)
		if typ == Ident {
			aliasedTypeRef := g.findTypeReference(g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, typeRef.pkgName)
			keyType = aliasedTypeRef.fullName()
			if _, ok := orderedMap[keyType]; ok {
				hashBody.WriteString(fmt.Sprintf("\tproptools.SortOrdered(%s)\n", keysVar))
			}
		} else {
			// Assume implementing Comparer interface.
			hashBody.WriteString(fmt.Sprintf("\tproptools.SortCustom(%s)\n", keysVar))
		}
	}

	// 3. Hash in sorted order
	hashBody.WriteString(fmt.Sprintf("\tfor _, %s := range %s {\n", iVar, keysVar))
	g.generateHashForType(hashBody, pkgName, t.Key, fmt.Sprintf("%s", iVar))
	g.generateHashForType(hashBody, pkgName, t.Value, fmt.Sprintf("%s[%s]", fieldName, iVar))
	hashBody.WriteString(fmt.Sprintf("\t}\n"))
}
