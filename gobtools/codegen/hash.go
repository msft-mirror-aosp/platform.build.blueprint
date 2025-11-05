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
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
)

func (g *gobGen) generateHash(pkgName string, structDecl *ast.TypeSpec, hashBody *strings.Builder) {
	structType, isStruct := structDecl.Type.(*ast.StructType)
	structName := structDecl.Name.Name

	fieldName := "r"
	hashBody.WriteString(fmt.Sprintf("func (%s %s) CustomHash(hasher *proptools.Hasher) error {\n", fieldName, structName))

	g.imports[proptoolsImport] = true

	if isStruct {
		g.generateHashForStruct(hashBody, pkgName, structName, structType, fieldName)
	} else {
		g.generateHashForType(hashBody, pkgName, structDecl.Type, fieldName)
	}

	hashBody.WriteString("\treturn nil\n")
	hashBody.WriteString("}\n")
}

func (g *gobGen) generateHashForType(hashBody *strings.Builder, pkgName string, field ast.Expr, fieldName string) {
	typeNameForHash := TypeToString(field)
	switch t := field.(type) {
	// cases such as "name string", "path Path", "basePath".
	case *ast.Ident:
		switch t.Name {
		case "string":
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", fieldName))
		case "bool":
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			hashBody.WriteString(fmt.Sprintf("\tif %s {\n", fieldName))
			hashBody.WriteString(fmt.Sprintf("\t\thasher.WriteByte(1)\n"))
			hashBody.WriteString(fmt.Sprintf("\t} else {\n"))
			hashBody.WriteString(fmt.Sprintf("\t\thasher.WriteByte(0)\n"))
			hashBody.WriteString(fmt.Sprintf("\t}\n"))
		case "int", "int8", "int16", "int32", "int64":
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteUint64(uint64(%s))\n", fieldName))
		case "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteUint64(uint64(%s))\n", fieldName))
		case "float32", "float64":
			g.imports[`"math"`] = true
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteUint64(mathasher.Float64bits(float64(%s)))\n", fieldName))
		case "any":
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			g.hashInterface(hashBody, fieldName, "any")
		default:
			typeRef := g.findTypeReference(t, pkgName)
			g.generateHashForCustomType(hashBody, fieldName, typeRef, g.getTypeNameForHash(pkgName, typeNameForHash))
		}
	case *ast.MapType:
		hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
		g.hashMap(hashBody, pkgName, t, fieldName)
	case *ast.ArrayType:
		hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
		if t.Len == nil { // Slice
			g.hashSlice(hashBody, pkgName, t.Elt, fieldName)
		} else { // Array
			g.hashArray(hashBody, pkgName, t.Elt, fieldName)
		}
	// pointers.
	case *ast.StarExpr:
		hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
		g.hashPointer(hashBody, pkgName, t, fieldName)
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
			g.hashUniqueList(hashBody, pkgName, t, fieldName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			g.hashDepSet(hashBody, pkgName, t, fieldName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok {
			if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "unique" && typ.Sel.Name == "Handle" {
				hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", g.getTypeNameForHash("", typeNameForHash)))
				g.hashUniqueHandle(hashBody, pkgName, t, fieldName)
			} else {
				hashBody.WriteString(fmt.Sprintf("\tif err := hasher.CalculateHashReflection(reflect.ValueOf(%s)); err != nil { return err }\n", fieldName))
				g.imports[reflectImport] = true
			}
		} else {
			hashBody.WriteString(fmt.Sprintf("\tif err := hasher.CalculateHashReflection(reflect.ValueOf(%s)); err != nil { return err }\n", fieldName))
			g.imports[reflectImport] = true
		}
	// type from other package such as "path android.Path".
	case *ast.SelectorExpr:
		typeRef := g.findTypeReference(t, pkgName)
		g.generateHashForCustomType(hashBody, fieldName, typeRef, g.getTypeNameForHash(pkgName, typeNameForHash))
	// anonymous struct
	case *ast.StructType:
		g.generateHashForStruct(hashBody, pkgName, "", t, fieldName)
	default:
		panic(fmt.Errorf("unknown data type: %v %T", t, t))
	}
}

func (g *gobGen) generateHashForCustomType(hashBody *strings.Builder, fieldName string, typeRef typeReference, typeNameForHash string) {
	if typeRef.pkgName == "scanner" && typeRef.typeName == "Position" {
		g.imports[`"github.com/google/blueprint/proptools/scanner"`] = true
		hashBody.WriteString(fmt.Sprintf("\t// Skipping hash for scanner.Position field: %s\n", fieldName))
		return
	}

	typ := g.findType(typeRef)
	if fieldName[len(fieldName)-1] == '.' {
		fieldName += typeRef.typeName
	}
	switch typ {
	case Struct:
		// For a type declaration like "type TypeIdent TypeStruct", we must hash the new type's
		// name ("TypeIdent") at this step. This is to distinguish it from other declarations that
		// might use the same underlying type, for example, "type NewStruct TypeStruct".
		// The underlying type ("TypeStruct") will be hashed separately by its CustomHash method.
		// This combined approach differs from the previous reflection-based method, which only hashed
		// the TypeIdent.
		newFieldName := removeOuterCast(fieldName)
		if newFieldName != fieldName {
			hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", typeNameForHash))
		}
		hashBody.WriteString(fmt.Sprintf("\tif err := %s.CustomHash(hasher); err != nil { return err }\n", fieldName))
	case Interface:
		hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", typeNameForHash))
		g.hashInterface(hashBody, fieldName, typeRef.fullName())
	case Ident:
		hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", typeNameForHash))
		// type declarations from other type, for example, "type TypeBasic int", "type TypeIdent TypeStruct"
		aliasedTypeRef := g.findTypeReference(g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, typeRef.pkgName)
		g.maybeAddImport(typeRef)
		g.maybeAddImport(aliasedTypeRef)
		newFieldName := fmt.Sprintf("%s(%s)", aliasedTypeRef.fullName(), fieldName)
		g.generateHashForType(hashBody, aliasedTypeRef.pkgName, g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, newFieldName)
	default:
		hashBody.WriteString(fmt.Sprintf("\thasher.WriteString(%s)\n", typeNameForHash))
		g.generateHashForType(hashBody, typeRef.pkgName, g.pkgStructs[typeRef.pkgName][typeRef.typeName].Type, fieldName)
	}
}

func (g *gobGen) hashInterface(hashBody *strings.Builder, fieldName string, typeName string) {
	isNil := g.nextVar()
	hashBody.WriteString(fmt.Sprintf("\t%s := %s == nil\n", isNil, fieldName))
	hashBody.WriteString(fmt.Sprintf("\tif %s {\n", isNil))
	hashBody.WriteString(fmt.Sprintf("\t\thasher.WriteByte(0)\n")) // 0 for nil
	hashBody.WriteString(fmt.Sprintf("\t} else {\n"))
	hashBody.WriteString(fmt.Sprintf("\tif v := reflect.ValueOf(%s); v.Kind() == reflect.Ptr {\n", fieldName))
	g.imports[reflectImport] = true
	hashBody.WriteString(fmt.Sprintf("\tif v.IsNil() {\n"))
	hashBody.WriteString(fmt.Sprintf("\tpanic(fmt.Errorf(\"nil pointer is not supported in interface\"))\n"))
	g.imports[`"fmt"`] = true
	hashBody.WriteString(fmt.Sprintf("\t} else {\n"))
	isNil = g.nextVar()
	hashBody.WriteString(fmt.Sprintf("\t%s := %s == nil\n", isNil, fieldName))
	hashBody.WriteString(fmt.Sprintf("\tif %s {\n", isNil))
	hashBody.WriteString(fmt.Sprintf("\t\thasher.WriteByte(0)\n")) // 0 for nil
	hashBody.WriteString(fmt.Sprintf("\t} else {\n"))
	hashFunc := g.nextVar()
	hashBody.WriteString(fmt.Sprintf("\t%s := func(hasher *proptools.Hasher) error { return %s.(proptools.CustomHash).CustomHash(hasher) }\n", hashFunc, fieldName))
	hashBody.WriteString(fmt.Sprintf("\tif err := proptools.HashReference(hasher, uintptr(v.Pointer()), %s); err != nil { return err }\n", hashFunc))
	hashBody.WriteString(fmt.Sprintf("\t}}} else {\n"))
	hashBody.WriteString(fmt.Sprintf("\t%s.(proptools.CustomHash).CustomHash(hasher)\n", fieldName))
	hashBody.WriteString(fmt.Sprintf("\t}}\n"))
}

func (g *gobGen) getTypeNameForHash(pkgName, typeName string) string {
	pkgPath, _ := g.importPkgs[pkgName]
	return fmt.Sprintf("\"%s:%s.%s\"", pkgPath, pkgName, typeName)
}

func removeOuterCast(s string) string {
	firstParen := strings.Index(s, "(")
	if firstParen <= 0 {
		return s
	}

	lastParen := strings.LastIndex(s, ")")

	if lastParen == -1 || lastParen < firstParen {
		return s
	}

	return s[firstParen+1 : lastParen]
}

func TypeToString(typ ast.Expr) string {
	fset := token.NewFileSet()
	var buf bytes.Buffer
	err := format.Node(&buf, fset, typ)
	if err != nil {
		panic(fmt.Errorf("failed to format ast.Expr: %w", err))
	}
	return buf.String()
}
