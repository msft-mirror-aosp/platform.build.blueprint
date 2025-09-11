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
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// This file provides functionality to auto-generate custom Encode and Decode
// method for Go structs.
//
// # Auto-Generation Trigger
//
// The generation process is triggered for structs annotated with the comment:
//   // @auto-generate: gob
// within a given Go source file.
//
// # Generated Methods
//
// 1. Encode(buf *bytes.Buffer) error:
//    Encodes each field within the struct into a stream of bytes.
//
// 2. Decode(buf *bytes.Reader) error:
//    Decodes from a stream of byte and populates each field within the struct.
//
// # Supported Data Types
//
// The generator can handle the following data types within the annotated structs:
//   - Most of the primitive types (e.g., int, string, bool)
//   - Pointers to supported types
//   - Maps (keys and values must be supported types)
//   - Slices of supported types
//   - Type aliases
//   - Interfaces.
//   - Other user defined structs (which should have generated Encode and Decode methods)

var verify = flag.Bool("verify", false, "verify existing outputs")

const genGobAnnotation = "@auto-generate: gob"
const blueprintPkgPrefix = "github.com/google/blueprint"
const blueprintPkgPath = "build/blueprint"
const soongPkgPrefix = "android/soong"
const soongPkgPath = "build/soong"
const gobtoolsImport = `"github.com/google/blueprint/gobtools"`
const valueIsNil = -1

type typeDefTypes int

const (
	Struct typeDefTypes = iota
	Alias
	Interface
	Slice
	Map
	Pointer
	Ident
	Unknown
)

type structMap map[string]*ast.TypeSpec

type typeReference struct {
	curPackage bool
	pkgName    string
	typeName   string
}

func (t typeReference) fullName() string {
	if t.curPackage {
		return t.typeName
	} else {
		return t.pkgName + "." + t.typeName
	}
}

type gobGen struct {
	pkgStructs map[string]structMap
	importPkgs map[string]string
	curPackage string
	sourceDir  string
	fieldId    int
	imports    map[string]bool
}

func newGobGen() *gobGen {
	return &gobGen{
		pkgStructs: make(map[string]structMap),
		importPkgs: make(map[string]string),
		imports:    make(map[string]bool),
	}
}

func (g *gobGen) findType(typeRef typeReference) typeDefTypes {
	if _, ok := g.pkgStructs[typeRef.pkgName]; !ok {
		g.importPackage(typeRef.pkgName, g.findPackagePath(typeRef.pkgName))
	}

	ts := g.pkgStructs[typeRef.pkgName][typeRef.typeName]
	if ts == nil {
		return Unknown
	}

	// 1. Check for Type Alias
	if ts.Assign.IsValid() {
		return Alias
	}

	// 2. If not an alias, inspect ts.Type
	switch ts.Type.(type) {
	case *ast.InterfaceType:
		return Interface
	case *ast.StructType:
		return Struct
	case *ast.Ident:
		return Ident
	case *ast.MapType:
		return Map
	case *ast.ArrayType:
		return Slice
	case *ast.StarExpr:
		return Pointer
	}
	return Unknown
}

func (g *gobGen) findTypeReference(expr ast.Expr, pkgName string) typeReference {
	var typeRef typeReference
	switch t := expr.(type) {
	case *ast.Ident:
		typeRef.pkgName = pkgName
		typeRef.typeName = t.Name
	case *ast.SelectorExpr:
		typeRef.pkgName = t.X.(*ast.Ident).Name
		typeRef.typeName = t.Sel.Name
	case *ast.ArrayType:
		typeRef = g.findTypeReference(t.Elt, pkgName)
		typeRef.typeName = "[]" + typeRef.typeName
		return typeRef
	case *ast.StarExpr:
		typeRef = g.findTypeReference(t.X, pkgName)
		typeRef.typeName = "*" + typeRef.typeName
		return typeRef
	default:
		panic(fmt.Errorf("unknown type to find name: %T", expr))
	}

	typeRef.curPackage = typeRef.pkgName == g.curPackage

	return typeRef
}

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

func (g *gobGen) generateEncodeForType(encodeBody *strings.Builder, pkgName string, field ast.Expr, fieldName string) {
	g.imports[`"bytes"`] = true

	switch t := field.(type) {
	// cases such as "name string", "path Path", "basePath".
	case *ast.Ident:
		switch t.Name {
		case "string":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeString(buf, %s); err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		case "int":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int64(%s)); err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		case "bool", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, %s); err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		case "any":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeInterface(ctx, buf, %s); err != nil { return err }\n", fieldName))
			g.imports[gobtoolsImport] = true
		default:
			typeRef := g.findTypeReference(t, pkgName)
			g.generateEncodeForCustomType(encodeBody, fieldName, typeRef)
		}
	case *ast.MapType:
		g.generateEncodeForNillable(encodeBody, fieldName)
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(len(%s))); err != nil { return err }\n", fieldName))
		encodeBody.WriteString(fmt.Sprintf("\tfor k, v := range %s {\n", fieldName))
		g.generateEncodeForType(encodeBody, pkgName, t.Key, "k")
		g.generateEncodeForType(encodeBody, pkgName, t.Value, "v")
		encodeBody.WriteString("\t}\n")
		encodeBody.WriteString("\t}\n")
	case *ast.ArrayType:
		g.encodeArray(encodeBody, pkgName, t.Elt, fieldName)
	// pointers.
	case *ast.StarExpr:
		isNil := g.nextVar()
		encodeBody.WriteString(fmt.Sprintf("\t%s := %s == nil\n", isNil, fieldName))
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, %s); err != nil { return err }\n", isNil))
		encodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isNil))
		g.generateEncodeForType(encodeBody, pkgName, t.X, "(*"+fieldName+")")
		encodeBody.WriteString("\t}\n")
	// generic types.
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			listName := g.nextVar()
			encodeBody.WriteString(fmt.Sprintf("\t%s := %s.ToSlice()\n", listName, fieldName))
			g.encodeArray(encodeBody, pkgName, t.Index, listName)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			typeRef := g.findTypeReference(t.Index, pkgName)
			if typeRef.typeName == "string" {
				encodeBody.WriteString(fmt.Sprintf("\tif err = %s.EncodeString(ctx, buf); err != nil { return err }\n", fieldName))
			} else if g.findType(typeRef) == Interface {
				encodeBody.WriteString(fmt.Sprintf("\tif err = %s.EncodeInterface(ctx, buf); err != nil { return err }\n", fieldName))
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

func (g *gobGen) generateEncodeForStruct(encodeBody *strings.Builder, pkgName string, t *ast.StructType, fieldName string) {
	for _, f := range t.Fields.List {
		encodeBody.WriteString("\n")
		fName := fieldName + "."
		if len(f.Names) > 0 {
			fName += f.Names[0].Name
		}
		g.generateEncodeForType(encodeBody, pkgName, f.Type, fName)
	}
}

func (g *gobGen) generateEncodeForNillable(encodeBody *strings.Builder, fieldName string) {
	encodeBody.WriteString(fmt.Sprintf("\tif %s == nil {\n", fieldName))
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(%d)); err != nil { return err }\n", valueIsNil))
	encodeBody.WriteString(fmt.Sprintf("\t} else {\n"))
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
		case "int":
			decodeBody.WriteString(fmt.Sprintf("\tvar %s int64\n", valId))
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int64](buf, &%s); if err != nil { return err }\n", valId))
			decodeBody.WriteString(fmt.Sprintf("\t%s = int(%s)\n", fieldName, valId))
			g.imports[gobtoolsImport] = true
		case "bool", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[%s](buf, &%s); if err != nil { return err }\n", t.Name, fieldName))
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
		kTypeRef := g.findTypeReference(t.Key, pkgName)
		vTypeRef := g.findTypeReference(t.Value, pkgName)
		decodeBody.WriteString(fmt.Sprintf("\tvar %s int32\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int32](buf, &%s); if err != nil { return err }\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\tif %s != %d {\n", valId, valueIsNil))
		decodeBody.WriteString(fmt.Sprintf("\t%s = make(map[%s]%s, %s)\n", fieldName, kTypeRef.fullName(), vTypeRef.fullName(), valId))
		g.maybeAddImport(kTypeRef)
		g.maybeAddImport(vTypeRef)
		index := g.nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < int(%s); %s++ {\n", index, index, valId, index))
		decodeBody.WriteString(fmt.Sprintf("\tvar k %s\n", kTypeRef.fullName()))
		decodeBody.WriteString(fmt.Sprintf("\tvar v %s\n", vTypeRef.fullName()))
		g.maybeAddImport(kTypeRef)
		g.maybeAddImport(vTypeRef)
		g.generateDecodeForType(decodeBody, pkgName, t.Key, "k")
		g.generateDecodeForType(decodeBody, pkgName, t.Value, "v")
		decodeBody.WriteString(fmt.Sprintf("\t%s[k] = v\n", fieldName))
		decodeBody.WriteString("\t}\n")
		decodeBody.WriteString("\t}\n")
	case *ast.ArrayType:
		g.decodeArray(decodeBody, pkgName, t.Elt, fieldName)
	case *ast.StarExpr:
		isNil := g.nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tvar %s bool\n", isNil))
		decodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.DecodeSimple(buf, &%s); err != nil { return err }\n", isNil))
		decodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isNil))
		typeRef := g.findTypeReference(t.X, pkgName)
		decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", valId, typeRef.fullName()))
		g.maybeAddImport(typeRef)
		g.generateDecodeForType(decodeBody, pkgName, t.X, valId)
		decodeBody.WriteString(fmt.Sprintf("\t%s = &%s\n", fieldName, valId))
		decodeBody.WriteString("\t}\n")
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			listName := g.nextVar()
			typeRef := g.findTypeReference(t.Index, pkgName)
			decodeBody.WriteString(fmt.Sprintf("\tvar %s []%s\n", listName, typeRef.fullName()))
			g.maybeAddImport(typeRef)
			g.decodeArray(decodeBody, pkgName, t.Index, listName)
			decodeBody.WriteString(fmt.Sprintf("\t%s = uniquelist.Make(%s)\n", fieldName, listName))
			g.imports[`"github.com/google/blueprint/uniquelist"`] = true
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			typeRef := g.findTypeReference(t.Index, pkgName)
			if typeRef.typeName == "string" {
				decodeBody.WriteString(fmt.Sprintf("\tif err = %s.DecodeString(ctx, buf); err != nil { return err }\n", fieldName))
			} else if g.findType(typeRef) == Interface {
				decodeBody.WriteString(fmt.Sprintf("\tif err = %s.DecodeInterface(ctx, buf); err != nil { return err }\n", fieldName))
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

func (g *gobGen) generateDecodeForStruct(decodeBody *strings.Builder, pkgName string, t *ast.StructType, fieldName string) {
	for _, f := range t.Fields.List {
		decodeBody.WriteString("\n")
		fName := fieldName + "."
		if len(f.Names) > 0 {
			fName += f.Names[0].Name
		}
		g.generateDecodeForType(decodeBody, pkgName, f.Type, fName)
	}
}

func (g *gobGen) encodeArray(encodeBody *strings.Builder, pkgName string, t ast.Expr, fieldName string) {
	g.generateEncodeForNillable(encodeBody, fieldName)
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(len(%s))); err != nil { return err }\n", fieldName))
	index := g.nextVar()
	encodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < len(%s); %s++ {\n", index, index, fieldName, index))
	g.generateEncodeForType(encodeBody, pkgName, t, fmt.Sprintf("%s[%s]", fieldName, index))
	encodeBody.WriteString("\t}\n")
	encodeBody.WriteString("\t}\n")
}

func (g *gobGen) decodeArray(decodeBody *strings.Builder, pkgName string, t ast.Expr, fieldName string) {
	valId := g.nextVar()
	typeRef := g.findTypeReference(t, pkgName)
	decodeBody.WriteString(fmt.Sprintf("\tvar %s int32\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int32](buf, &%s); if err != nil { return err }\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\tif %s != %d {\n", valId, valueIsNil))
	decodeBody.WriteString(fmt.Sprintf("\t%s = make([]%s, %s)\n", fieldName, typeRef.fullName(), valId))
	g.maybeAddImport(typeRef)
	index := g.nextVar()
	decodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < int(%s); %s++ {\n", index, index, valId, index))
	g.generateDecodeForType(decodeBody, pkgName, t, fmt.Sprintf("%s[%s]", fieldName, index))
	decodeBody.WriteString("\t}\n")
	decodeBody.WriteString("\t}\n")
}

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

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [sources]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	sources := slices.Clone(flag.Args())

	if f := os.Getenv("GOFILE"); f != "" {
		sources = append(sources, f)
	}

	if len(sources) == 0 {
		flag.Usage()
	}

	g := newGobGen()
	curDir, _ := os.Getwd()
	parts := strings.Split(curDir, blueprintPkgPath)
	if len(parts) < 2 {
		parts = strings.Split(curDir, soongPkgPath)
	}
	if len(parts) < 2 {
		// verify mode, the current directory is the base of the source tree.
		g.sourceDir = curDir
	} else {
		g.sourceDir = parts[0]
	}

	for _, s := range sources {
		out, err := g.generate(s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to generate output for %s: %s\n", s, err)
			os.Exit(1)
		}

		outputFile := strings.TrimSuffix(s, ".go") + "_gob_enc.go"
		if *verify {
			if len(out) == 0 {
				err := expectNotExist(outputFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "verification error: %s\n", err)
					os.Exit(1)
				}
			} else {
				if err := expectContents(outputFile, out); err != nil {
					fmt.Fprintf(os.Stderr, "verification error: %s\n", err)
					os.Exit(1)
				}
				if !slices.Contains(sources, outputFile) {
					fmt.Fprintf(os.Stderr, "verification error: generated file %s is not in srcs\n", outputFile)
					os.Exit(1)
				}
			}
		} else if len(out) > 0 {
			err = os.WriteFile(outputFile, out, 0666)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to write output for %s to %s: %s\n", s, outputFile, err)
				os.Exit(1)
			}
		}
	}
}

func (g *gobGen) findPackagePath(pkgName string) string {
	var pkgDir string
	baseDir := strings.TrimPrefix(g.importPkgs[pkgName], blueprintPkgPrefix)
	if baseDir != g.importPkgs[pkgName] {
		pkgDir = filepath.Join(g.sourceDir, blueprintPkgPath, baseDir)
	} else {
		baseDir = strings.TrimPrefix(g.importPkgs[pkgName], soongPkgPrefix)
		if baseDir != g.importPkgs[pkgName] {
			pkgDir = filepath.Join(g.sourceDir, soongPkgPath, baseDir)
		}
	}
	if pkgDir == "" {
		panic(fmt.Errorf("failed to find the package path: %s", g.importPkgs[pkgName]))
	}

	return pkgDir
}

func (g *gobGen) importPackage(pkgName string, pkgDir string) {
	if _, ok := g.pkgStructs[pkgName]; ok {
		return
	}
	includePattern := "*.go"
	excludePattern := "*_test.go"

	fullPattern := filepath.Join(pkgDir, includePattern)

	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		panic(fmt.Errorf("error matching include pattern: %v", err))
	}

	if len(matches) == 0 {
		panic(fmt.Errorf("No files found matching pattern '%s' in directory '%s'\n", includePattern, pkgDir))
		return
	}

	for _, match := range matches {
		ok, err := filepath.Match(excludePattern, match)
		if err != nil {
			panic(fmt.Errorf("error matching exclude pattern: %v", err))
		}
		if ok {
			continue
		}
		node, err := parseFile(match)
		if err != nil {
			panic(fmt.Errorf("failed to parse file: %s", match))
		}
		g.loadTypes(node)
	}
}

func parseFile(source string) (*ast.File, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, source, nil, parser.ParseComments)
	if err != nil {
		// ParseFile might return multiple errors in the form of a scanner.ErrorList.  By default printing the error
		// only shows the first error.  Verification may happen very early during the build, so this may be the first
		// time syntax errors are reported.  Use scanner.PrintError to convert them into a single error that contains
		// all the error lines to make the errors more actionable.
		if errorList, ok := err.(scanner.ErrorList); ok {
			var buf bytes.Buffer
			scanner.PrintError(&buf, errorList)
			err = errors.New(buf.String())
		}
		return nil, fmt.Errorf("failed to parse:\n%w", err)
	}
	return node, nil
}

func (g *gobGen) generateRegistry(structDecl *ast.TypeSpec, codeBody *strings.Builder, initCodeBody *strings.Builder) {
	structName := structDecl.Name.Name
	typeId := structName + "GobRegId"
	codeBody.WriteString(fmt.Sprintf("\tvar %s int16\n", typeId))
	codeBody.WriteString("func (r " + structName + ") GetTypeId() int16 {\n")
	codeBody.WriteString(fmt.Sprintf("\treturn %s\n", typeId))
	codeBody.WriteString("}\n\n")

	initCodeBody.WriteString(fmt.Sprintf("\t%s = gobtools.RegisterType(func() gobtools.CustomDec { return new(%s) })\n", typeId, structName))
	g.imports[gobtoolsImport] = true
}

func (g *gobGen) generate(source string) ([]byte, error) {
	node, err := parseFile(source) // Find the file containing the struct
	if err != nil {
		return nil, err
	}
	for _, p := range node.Imports {
		pkgPath := strings.ReplaceAll(p.Path.Value, "\"", "")
		if strings.HasPrefix(pkgPath, blueprintPkgPrefix) || strings.HasPrefix(pkgPath, soongPkgPrefix) {
			pkgName := path.Base(pkgPath)
			if _, ok := g.importPkgs[pkgName]; !ok {
				g.importPkgs[pkgName] = pkgPath
			}
		}
	}
	g.curPackage = node.Name.Name
	g.importPackage(g.curPackage, path.Dir(source))

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by go run gob_gen.go; DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n", g.curPackage)
	fmt.Fprintln(&b, "import (")

	var codeBodies []*strings.Builder
	g.imports = make(map[string]bool)
	initCodeBody := &strings.Builder{}
	initCodeBody.WriteString("func init() {\n")
	structDecls := findStructs(node)
	for _, structDecl := range structDecls {
		g.fieldId = 0
		codeBody := &strings.Builder{}
		g.generateEncode(g.curPackage, structDecl, codeBody)
		codeBody.WriteString("\n")
		g.fieldId = 0
		g.generateDecode(g.curPackage, structDecl, codeBody)
		codeBodies = append(codeBodies, codeBody)
		g.generateRegistry(structDecl, codeBody, initCodeBody)
	}

	if len(codeBodies) == 0 {
		return nil, nil
	}

	initCodeBody.WriteString("}\n\n")

	fmt.Fprintln(&b, strings.Join(slices.Sorted(maps.Keys(g.imports)), "\n"))
	fmt.Fprintln(&b, ")")
	fmt.Fprintf(&b, initCodeBody.String())
	for _, codeBody := range codeBodies {
		fmt.Fprintf(&b, codeBody.String())
		fmt.Fprintln(&b)
	}

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("source format error: %w", err)
	}

	return out, nil
}

// expectContents verifies the that file contains the given bytes, returning an error that describes
// how to fix the problem if it does not.
func expectContents(file string, expected []byte) error {
	actual, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return fmt.Errorf("generated file %s does not exist, rerun `go generate` in %s",
			file, filepath.Dir(file))
	}
	if err != nil {
		return err
	}

	if len(expected) == 0 {
		return fmt.Errorf("found unexpected generated file %s, delete it", file)
	}

	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("generated file %s has out of date contents, rerun `go generate` in %s",
			file, filepath.Dir(file))
	}

	return nil
}

// expectNotExist verifies that the file does not exist, returning an error that describes how to
// fix the problem if it does.
func expectNotExist(file string) error {
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("expected %s to not exist, delete it", file)
}
