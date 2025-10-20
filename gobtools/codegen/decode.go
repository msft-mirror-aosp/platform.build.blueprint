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

func (g *gobGen) generateDecode(pkgName string, structDecl *ast.TypeSpec, decodeBody *strings.Builder) {
	structType, isStruct := structDecl.Type.(*ast.StructType)
	structName := structDecl.Name.Name

	decodeBody.WriteString("func (r *" + structName + ") Decode(ctx gobtools.EncContext, buf *bytes.Reader) error {\n")
	decodeBody.WriteString("\tvar err error\n")

	if isStruct {
		g.generateDecodeForStruct(decodeBody, pkgName, structType, "r")
	} else {
		fieldName := "(*r)"
		decodeBody.WriteString("\n")
		g.generateDecodeForType(decodeBody, pkgName, structDecl.Type, fieldName)
	}

	decodeBody.WriteString("\n\treturn err\n")
	decodeBody.WriteString("}\n")
}

func (g *gobGen) generateDecodeForCustomType(decodeBody *strings.Builder, fieldName string, typeRef typeReference) {
	typ := g.findType(typeRef)
	if fieldName[len(fieldName)-1] == '.' {
		fieldName += typeRef.typeName
	}

	switch typ {
	case Struct:
		decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(ctx, buf); err != nil { return err }\n", fieldName))
	case Interface:
		tmpVar := g.nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tif %s, err := gobtools.DecodeInterface(ctx, buf); err != nil { return err } else if %s == nil {\n", tmpVar, tmpVar))
		decodeBody.WriteString(fmt.Sprintf("\t%s = nil } else {\n", fieldName))
		decodeBody.WriteString(fmt.Sprintf("\t%s = %s.(%s) }\n", fieldName, tmpVar, typeRef.fullName()))
		g.imports[gobtoolsImport] = true
		g.maybeAddImport(typeRef)
	case Ident:
		// type alias declarations such as "type OsClass int".
		aliasedTypeRef := g.findTypeReference(g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, typeRef.pkgName)
		tmpVar := g.nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", tmpVar, aliasedTypeRef.fullName()))
		g.generateDecodeForType(decodeBody, aliasedTypeRef.pkgName, g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, tmpVar)
		decodeBody.WriteString(fmt.Sprintf("\t%s = %s(%s)\n", fieldName, typeRef.fullName(), tmpVar))
		g.maybeAddImport(typeRef)
		g.maybeAddImport(aliasedTypeRef)
	default:
		g.generateDecodeForType(decodeBody, typeRef.pkgName, g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, fieldName)
	}
}

func (g *gobGen) generateDecodeForType(decodeBody *strings.Builder, pkgName string, field ast.Expr, fieldName string) {
	valId := g.nextVar()

	g.imports[`"bytes"`] = true

	switch t := field.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeString(buf, &%s); if err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		case "bool", "int", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			decodeBody.WriteString(fmt.Sprintf("\terr = %s(buf, &%s); if err != nil { return err }\n", integerTypeToDecoder(t.Name), fieldName))
			g.imports[gobtoolsImport] = true
		case "any":
			tmpVar := g.nextVar()
			decodeBody.WriteString(fmt.Sprintf("\tif %s, err := gobtools.DecodeInterface(ctx, buf); err != nil { return err } else if %s == nil {\n", tmpVar, tmpVar))
			decodeBody.WriteString(fmt.Sprintf("\t%s = nil } else {\n", fieldName))
			decodeBody.WriteString(fmt.Sprintf("\t%s = %s }\n", fieldName, tmpVar))
			g.imports[gobtoolsImport] = true
		default:
			typeRef := g.findTypeReference(t, pkgName)
			g.generateDecodeForCustomType(decodeBody, fieldName, typeRef)
		}
	case *ast.MapType:
		g.decodeMap(decodeBody, pkgName, t, valId, fieldName)
	case *ast.ArrayType:
		if t.Len == nil {
			g.decodeSlice(decodeBody, pkgName, t.Elt, fieldName)
		} else {
			g.decodeArray(decodeBody, pkgName, t.Elt, fieldName)
		}
	case *ast.StarExpr:
		g.decodePointer(decodeBody, pkgName, t, valId, fieldName)
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			g.decodeUniqueList(decodeBody, pkgName, t, fieldName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			g.decodeDepSet(decodeBody, pkgName, t, fieldName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok {
			if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "unique" && typ.Sel.Name == "Handle" {
				g.decodeUniqueHandle(decodeBody, pkgName, t, fieldName)
			} else {
				decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(ctx, buf); err != nil { return err }\n", fieldName))
			}
		} else {
			decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(ctx, buf); err != nil { return err }\n", fieldName))
		}
	case *ast.SelectorExpr:
		typeRef := g.findTypeReference(t, pkgName)
		g.generateDecodeForCustomType(decodeBody, fieldName, typeRef)
	// anonymous struct
	case *ast.StructType:
		g.generateDecodeForStruct(decodeBody, pkgName, t, fieldName)
	default:
		panic(fmt.Errorf("unknown data type: %v %T", t, t))
	}
}

func (g *gobGen) generateDecodeForNillable(decodeBody *strings.Builder, valId string) {
	decodeBody.WriteString(fmt.Sprintf("\tvar %s int\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeInt(buf, &%s); if err != nil { return err }\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\tif %s != %d {\n", valId, valueIsNil))
}

func integerTypeToDecoder(t string) string {
	switch t {
	case "bool", "int", "int16", "int32", "int64", "uint16", "uint32", "uint64":
		return "gobtools.Decode" + strings.ToUpper(t[:1]) + t[1:]
	default:
		panic(fmt.Errorf("unknown integer type: %s", t))
	}
}
