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

func (g *gobGen) encodeUniqueHandle(encodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	isZeroValue := g.nextVar()
	typeRef := g.findTypeReference(t.Index, pkgName)
	encodeBody.WriteString(fmt.Sprintf("\t %s := %s == unique.Handle[%s]{}\n", isZeroValue, fieldName, typeRef.fullName()))
	g.maybeAddImport(typeRef)
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeBool(buf, %s); err != nil { return err }\n", isZeroValue))
	encodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isZeroValue))
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeReference(ctx, %s, buf, func(v unique.Handle[%s], buf *bytes.Buffer) error {\n", fieldName, typeRef.fullName()))
	encodeBody.WriteString(fmt.Sprintf("\treturn v.Value().Encode(ctx, buf)\n"))
	encodeBody.WriteString(fmt.Sprintf("\t}); err != nil { return err }\n"))
	encodeBody.WriteString(fmt.Sprintf("\t}\n"))
	g.imports[`"unique"`] = true
}

func (g *gobGen) decodeUniqueHandle(decodeBody *strings.Builder, pkgName string, t *ast.IndexExpr, fieldName string) {
	isZeroValue := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\tvar %s bool\n", isZeroValue))
	decodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.DecodeBool(buf, &%s); err != nil { return err }\n", isZeroValue))
	decodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isZeroValue))
	typeRef := g.findTypeReference(t.Index, pkgName)
	varName := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\ttmp, err := gobtools.DecodeReference(ctx, &%s, buf, func(value *unique.Handle[%s], buf *bytes.Reader) error {\n", fieldName, typeRef.fullName()))
	decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", varName, typeRef.fullName()))
	g.maybeAddImport(typeRef)
	decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(ctx, buf); err != nil { return err }\n", varName))
	decodeBody.WriteString(fmt.Sprintf("\t*value = unique.Make(%s)\n", varName))
	decodeBody.WriteString(fmt.Sprintf("\treturn nil\n"))
	decodeBody.WriteString(fmt.Sprintf("\t})\n"))
	decodeBody.WriteString(fmt.Sprintf("\tif err != nil { return err }\n"))
	decodeBody.WriteString(fmt.Sprintf("\t%s = *tmp\n", fieldName))
	decodeBody.WriteString(fmt.Sprintf("\t}\n"))
	g.imports[`"unique"`] = true
}
