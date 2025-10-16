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

func (g *gobGen) generateEncode(pkgName string, structDecl *ast.TypeSpec, encodeBody *strings.Builder) {
	structType, isStruct := structDecl.Type.(*ast.StructType)
	structName := structDecl.Name.Name

	encodeBody.WriteString("func (r " + structName + ") Encode(ctx gobtools.EncContext, buf *bytes.Buffer) error {\n")
	encodeBody.WriteString("\tvar err error\n")

	if isStruct {
		g.generateEncodeForStruct(encodeBody, pkgName, structType, "r")
	} else {
		fieldName := "r"
		encodeBody.WriteString("\n")
		g.generateEncodeForType(encodeBody, pkgName, structDecl.Type, fieldName)
	}

	encodeBody.WriteString("\treturn err\n")
	encodeBody.WriteString("}\n")
}

func (g *gobGen) generateEncodeForCustomType(encodeBody *strings.Builder, fieldName string, typeRef typeReference) {
	typ := g.findType(typeRef)
	if fieldName[len(fieldName)-1] == '.' {
		fieldName += typeRef.typeName
	}
	switch typ {
	case Struct:
		encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(ctx, buf); err != nil { return err }\n", fieldName))
	case Interface:
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeInterface(ctx, buf, %s); err != nil { return err }\n", fieldName))
		g.imports[gobtoolsImport] = true
	case Ident:
		// type alias declarations such as "type OsClass int".
		aliasedTypeRef := g.findTypeReference(g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, typeRef.pkgName)
		g.maybeAddImport(typeRef)
		g.maybeAddImport(aliasedTypeRef)
		newFieldName := fmt.Sprintf("%s(%s)", aliasedTypeRef.fullName(), fieldName)
		g.generateEncodeForType(encodeBody, aliasedTypeRef.pkgName, g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, newFieldName)
	default:
		g.generateEncodeForType(encodeBody, typeRef.pkgName, g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, fieldName)
	}
}

func (g *gobGen) generateEncodeForType(encodeBody *strings.Builder, pkgName string, field ast.Expr, fieldName string) {
	g.imports[`"bytes"`] = true

	switch t := field.(type) {
	// cases such as "name string", "path Path", "basePath".
	case *ast.Ident:
		switch t.Name {
		case "string":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeString(buf, %s); err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		case "bool", "int", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			encodeBody.WriteString(fmt.Sprintf("\tif err = %s(buf, %s); err != nil { return err }\n", integerTypeToEncoder(t.Name), fieldName))
			g.imports[gobtoolsImport] = true
		case "any":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeInterface(ctx, buf, %s); err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		default:
			typeRef := g.findTypeReference(t, pkgName)
			g.generateEncodeForCustomType(encodeBody, fieldName, typeRef)
		}
	case *ast.MapType:
		g.encodeMap(encodeBody, pkgName, t, fieldName)
	case *ast.ArrayType:
		if t.Len == nil {
			g.encodeSlice(encodeBody, pkgName, t.Elt, fieldName)
		} else {
			g.encodeArray(encodeBody, pkgName, t.Elt, fieldName)
		}
	// pointers.
	case *ast.StarExpr:
		g.encodePointer(encodeBody, pkgName, t, fieldName)
	// generic types.
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			g.encodeUniqueList(encodeBody, pkgName, t, fieldName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			g.encodeDepSet(encodeBody, pkgName, t, fieldName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok {
			if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "unique" && typ.Sel.Name == "Handle" {
				g.encodeUniqueHandle(encodeBody, pkgName, t, fieldName)
			} else {
				encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(ctx, buf); err != nil { return err }\n", fieldName))
			}
		} else {
			encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(ctx, buf); err != nil { return err }\n", fieldName))
		}
	// type from other package such as "path android.Path".
	case *ast.SelectorExpr:
		typeRef := g.findTypeReference(t, pkgName)
		g.generateEncodeForCustomType(encodeBody, fieldName, typeRef)
	// anonymous struct
	case *ast.StructType:
		g.generateEncodeForStruct(encodeBody, pkgName, t, fieldName)
	default:
		panic(fmt.Errorf("unknown data type: %v %T", t, t))
	}
}

func (g *gobGen) generateEncodeForNillable(encodeBody *strings.Builder, fieldName string) {
	encodeBody.WriteString(fmt.Sprintf("\tif %s == nil {\n", fieldName))
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeInt(buf, %d); err != nil { return err }\n", valueIsNil))
	encodeBody.WriteString(fmt.Sprintf("\t} else {\n"))
}

func integerTypeToEncoder(t string) string {
	switch t {
	case "bool", "int", "int16", "int32", "int64", "uint16", "uint32", "uint64":
		return "gobtools.Encode" + strings.ToUpper(t[:1]) + t[1:]
	default:
		panic(fmt.Errorf("unknown integer type: %s", t))
	}
}
