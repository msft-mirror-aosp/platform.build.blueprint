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

// This file provides functionality to auto-generate GobEncode, GobDecode,
// and a custom Decode method for Go structs.
//
// # Auto-Generation Trigger
//
// The generation process is triggered for structs annotated with the comment:
//   // @auto-generate: gob
// within a given Go source file.
//
// # Generated Methods
//
// 1. GobEncode() ([]byte, error):
//    Encodes each field within the struct into a stream of bytes.
//
// 2. GobDecode(b []byte) error:
//    Decodes from a stream of byte and populates each field within the struct.
//
// 3. Decode(buf *bytes.Reader) error:
//    This custom Decode method differs from GobDecode. It takes a bytes.Reader
//    instead of a []byte so this method can be called sequentially on multiple
//    fields without the needing to move the cursor on the []byte that represents
//    the whole struct.
//
// # Supported Data Types
//
// The generator can handle the following data types within the annotated structs:
//   - Most of the primitive types (e.g., int, string, bool)
//   - Pointers to supported types
//   - Maps (keys and values must be supported types)
//   - Slices of supported types
//   - Other structs (which should ideally also have generated or standard gob methods)
//
// # Handling of Type Aliases and Interfaces
//
// For fields that are type aliases or interface types, the generated methods
// partially rely on the standard `encoding/gob` package's behavior:
//
//   - Type Aliases: The `encoding/gob` package handles type aliases
//     transparently, the actual encoding and decoding work of the underlying type
//     will be handled by the generated code.
//
//   - Interfaces: When a field of an interface type holds a concrete type,
//     the `encoding/gob` package handles the registration and instantiation
//     of the concrete type:
//       - Encoding: Gob stores metadata about both the interface type and the
//         actual concrete type of the value assigned to the interface field.
//       - Decoding: Gob uses the stored metadata to instantiate an object of the
//         correct concrete type and assign it to the interface field.
//
//     The generated GobEncode/GobDecode methods for the struct containing such an
//     interface field will delegate the encoding/decoding of the field to gob.
//     If that concrete type itself has generated gob methods
//     (due to its own `@auto-generate: gob` annotation), those generated methods
//     will be invoked by gob during the process.
//
// # Example Workflow
//
// Consider a struct `MyData`:
//
//   // @auto-generate: gob
//   type MyData struct {
//       ID   int
//       Name string
//       Extra interface{}
//   }
//
//   // @auto-generate: gob
//   type ConcreteExtra struct {
//       Value int64
//   }
//
//   func main() {
//       data := MyData{ID: 1, Name: "Test", Extra: &ConcreteExtra{Value: 3}}
//       // ... encoding/decoding using generated methods ...
//   }
//
// During encoding of `data.Extra`:
//   1. The generated `MyData.GobEncode` method will encounter the `Extra` field.
//   2. It will delegate to `gob.Encoder.Encode(data.Extra)`.
//   3. `gob` will record that `Extra` is of type `interface{}` and holds a `*ConcreteExtra`.
//   4. `gob` will then call the `GobEncode` method of `*ConcreteExtra` (which would
//      also be auto-generated in this example) to encode its `Value` field.
//
// During decoding:
//   1. The generated `MyData.GobDecode` method will delegate to `gob.Decoder.Decode(&data.Extra)`.
//   2. `gob` will read the type information, instantiate a new `*ConcreteExtra`,
//      and call its `GobDecode` method to populate its fields.
//   3. The newly decoded `*ConcreteExtra` will be assigned to `data.Extra`.
//

var verify = flag.Bool("verify", false, "verify existing outputs")

var fieldId int

const genGobAnnotation = "@auto-generate: gob"
const blueprintPkgPrefix = "github.com/google/blueprint"
const blueprintPkgPath = "build/blueprint"
const soongPkgPrefix = "android/soong"
const soongPkgPath = "build/soong"

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

var pkgStructs = make(map[string]structMap)
var importPkgs = make(map[string]string)
var curPackage string
var sourceDir string

func findType(pkgName string, typeName string, imports map[string]bool) typeDefTypes {
	if _, ok := pkgStructs[pkgName]; !ok {
		importPackage(pkgName, findPackagePath(pkgName, imports))
	}

	ts := pkgStructs[pkgName][typeName]
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

func findStructName(expr ast.Expr, pkgName string) (string, string, string) {
	var typeName string
	var fullName string
	switch t := expr.(type) {
	case *ast.Ident:
		typeName = t.Name
	case *ast.SelectorExpr:
		pkgName = t.X.(*ast.Ident).Name
		typeName = t.Sel.Name
	case *ast.ArrayType:
		pkgName, typeName, _ = findStructName(t.Elt, pkgName)
		typeName = "[]" + typeName
	default:
		panic(fmt.Errorf("unknown type to find name: %T", expr))
	}
	if pkgName != curPackage {
		fullName = pkgName + "." + typeName
	} else {
		fullName = typeName
	}

	return pkgName, typeName, fullName
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

func loadTypes(node *ast.File) {
	pkgName := node.Name.Name
	if _, ok := pkgStructs[pkgName]; !ok {
		pkgStructs[pkgName] = make(map[string]*ast.TypeSpec)
	}
	for _, decl := range node.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
			for _, spec := range genDecl.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					pkgStructs[pkgName][typeSpec.Name.Name] = typeSpec
				}
			}
		}
	}
}

func maybeAddImport(structName string, imports map[string]bool) {
	if parts := strings.Split(structName, "."); len(parts) == 2 {
		imports[fmt.Sprintf("\"%s\"", importPkgs[parts[0]])] = true
	}
}

func nextVar() string {
	fieldId++
	return fmt.Sprintf("val%d", fieldId)
}

func generateEncodeForType(encodeBody *strings.Builder, pkgName string, field ast.Expr, fieldName string, imports map[string]bool) {
	imports[`"bytes"`] = true

	switch t := field.(type) {
	// cases such as "name string", "path Path", "basePath".
	case *ast.Ident:
		switch t.Name {
		case "string":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeString(buf, %s); err != nil { return err }\n", fieldName))
			imports[`"github.com/google/blueprint/gobtools"`] = true
		case "int":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int64(%s)); err != nil { return err }\n", fieldName))
			imports[`"github.com/google/blueprint/gobtools"`] = true
		case "bool", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, %s); err != nil { return err }\n", fieldName))
			imports[`"github.com/google/blueprint/gobtools"`] = true
		default:
			generateEncodeForCustomType(encodeBody, fieldName, pkgName, t.Name, imports)
		}
	case *ast.MapType:
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(len(%s))); err != nil { return err }\n", fieldName))
		encodeBody.WriteString(fmt.Sprintf("\tfor k, v := range %s {\n", fieldName))
		generateEncodeForType(encodeBody, pkgName, t.Key, "k", imports)
		generateEncodeForType(encodeBody, pkgName, t.Value, "v", imports)
		encodeBody.WriteString("\t}\n")
	case *ast.ArrayType:
		encodeArray(encodeBody, pkgName, t.Elt, fieldName, imports)
	// pointers.
	case *ast.StarExpr:
		isNil := nextVar()
		encodeBody.WriteString(fmt.Sprintf("\t%s := %s == nil\n", isNil, fieldName))
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, %s); err != nil { return err }\n", isNil))
		encodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isNil))
		generateEncodeForType(encodeBody, pkgName, t.X, "(*"+fieldName+")", imports)
		encodeBody.WriteString("\t}\n")
	// generic types.
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			listName := nextVar()
			encodeBody.WriteString(fmt.Sprintf("\t%s := %s.ToSlice()\n", listName, fieldName))
			encodeArray(encodeBody, pkgName, t.Index, listName, imports)
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			pkgName, typName, _ := findStructName(t.Index, pkgName)
			if typName == "string" {
				encodeBody.WriteString(fmt.Sprintf("\tif err = %s.EncodeString(buf); err != nil { return err }\n", fieldName))
			} else if findType(pkgName, typName, imports) == Interface {
				encodeBody.WriteString(fmt.Sprintf("\tif err = %s.EncodeInterface(buf); err != nil { return err }\n", fieldName))
			} else {
				encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(buf); err != nil { return err }\n", fieldName))
			}
		} else {
			encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(buf); err != nil { return err }\n", fieldName))
		}
	// type from other package such as "path android.Path".
	case *ast.SelectorExpr:
		pkgName, typName, _ := findStructName(t, pkgName)
		generateEncodeForCustomType(encodeBody, fieldName, pkgName, typName, imports)
	// anonymous struct
	case *ast.StructType:
		for _, f := range t.Fields.List {
			encodeBody.WriteString("\n")
			fName := fieldName + "."
			if len(f.Names) > 0 {
				fName += f.Names[0].Name
			}
			generateEncodeForType(encodeBody, pkgName, f.Type, fName, imports)
		}
	default:
		panic(fmt.Errorf("unknown data type: %v %T", t, t))
	}
}

func generateEncodeForCustomType(encodeBody *strings.Builder, fieldName string, pkgName string, typeName string, imports map[string]bool) {
	typ := findType(pkgName, typeName, imports)
	if fieldName[len(fieldName)-1] == '.' {
		fieldName += typeName
	}
	switch typ {
	case Struct:
		encodeBody.WriteString(fmt.Sprintf("\tif err = %s.Encode(buf); err != nil { return err }\n", fieldName))
	case Interface:
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeInterface(buf, %s); err != nil { return err }\n", fieldName))
		imports[`"github.com/google/blueprint/gobtools"`] = true
	case Ident:
		// new type declarations such as "type OsClass int".
		_, _, origType := findStructName(pkgStructs[pkgName][typeName].Type, pkgName)
		maybeAddImport(origType, imports)
		newFieldName := fmt.Sprintf("%s(%s)", origType, fieldName)
		generateEncodeForType(encodeBody, pkgName, pkgStructs[pkgName][typeName].Type, newFieldName, imports)
	default:
		generateEncodeForType(encodeBody, pkgName, pkgStructs[pkgName][typeName].Type, fieldName, imports)
	}
}

func generateDecodeForCustomType(decodeBody *strings.Builder, fieldName string, pkgName string, typeName string, imports map[string]bool) {
	typ := findType(pkgName, typeName, imports)
	if fieldName[len(fieldName)-1] == '.' {
		fieldName += typeName
	}
	fullName := typeName
	if pkgName != curPackage {
		fullName = pkgName + "." + typeName
	}

	switch typ {
	case Struct:
		decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(buf); err != nil { return err }\n", fieldName))
	case Interface:
		tmpVar := nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tif %s, err := gobtools.DecodeInterface(buf); err != nil { return err } else if %s == nil {\n", tmpVar, tmpVar))
		decodeBody.WriteString(fmt.Sprintf("\t%s = nil } else {\n", fieldName))
		decodeBody.WriteString(fmt.Sprintf("\t%s = %s.(%s) }\n", fieldName, tmpVar, fullName))
		imports[`"github.com/google/blueprint/gobtools"`] = true
		maybeAddImport(fullName, imports)
	case Ident:
		// new type declarations such as "type OsClass int".
		_, _, origType := findStructName(pkgStructs[pkgName][typeName].Type, pkgName)
		tmpVar := nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", tmpVar, origType))
		generateDecodeForType(decodeBody, pkgName, pkgStructs[pkgName][typeName].Type, tmpVar, imports)
		decodeBody.WriteString(fmt.Sprintf("\t%s = %s(%s)\n", fieldName, fullName, tmpVar))
		maybeAddImport(fullName, imports)
		maybeAddImport(origType, imports)
	default:
		generateDecodeForType(decodeBody, pkgName, pkgStructs[pkgName][typeName].Type, fieldName, imports)
	}
}

func generateDecodeForType(decodeBody *strings.Builder, pkgName string, field ast.Expr, fieldName string, imports map[string]bool) {
	valId := nextVar()

	imports[`"bytes"`] = true

	switch t := field.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeString(buf, &%s); if err != nil { return err }\n", fieldName))
			imports[`"github.com/google/blueprint/gobtools"`] = true
		case "int":
			decodeBody.WriteString(fmt.Sprintf("\tvar %s int64\n", valId))
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int64](buf, &%s); if err != nil { return err }\n", valId))
			decodeBody.WriteString(fmt.Sprintf("\t%s = int(%s)\n", fieldName, valId))
			imports[`"github.com/google/blueprint/gobtools"`] = true
		case "bool", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[%s](buf, &%s); if err != nil { return err }\n", t.Name, fieldName))
			imports[`"github.com/google/blueprint/gobtools"`] = true
		default:
			generateDecodeForCustomType(decodeBody, fieldName, pkgName, t.Name, imports)
		}
	case *ast.MapType:
		_, _, kName := findStructName(t.Key, pkgName)
		_, _, vName := findStructName(t.Value, pkgName)
		decodeBody.WriteString(fmt.Sprintf("\tvar %s int32\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int32](buf, &%s); if err != nil { return err }\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\tif %s > 0 {\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\t%s = make(map[%s]%s, %s)\n", fieldName, kName, vName, valId))
		maybeAddImport(kName, imports)
		maybeAddImport(vName, imports)
		index := nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < int(%s); %s++ {\n", index, index, valId, index))
		decodeBody.WriteString(fmt.Sprintf("\tvar k %s\n", kName))
		decodeBody.WriteString(fmt.Sprintf("\tvar v %s\n", vName))
		maybeAddImport(kName, imports)
		maybeAddImport(vName, imports)
		generateDecodeForType(decodeBody, pkgName, t.Key, "k", imports)
		generateDecodeForType(decodeBody, pkgName, t.Value, "v", imports)
		decodeBody.WriteString(fmt.Sprintf("\t%s[k] = v\n", fieldName))
		decodeBody.WriteString("\t}\n")
		decodeBody.WriteString("\t}\n")
	case *ast.ArrayType:
		decodeArray(decodeBody, pkgName, t.Elt, fieldName, imports)
	case *ast.StarExpr:
		isNil := nextVar()
		decodeBody.WriteString(fmt.Sprintf("\tvar %s bool\n", isNil))
		decodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.DecodeSimple(buf, &%s); err != nil { return err }\n", isNil))
		decodeBody.WriteString(fmt.Sprintf("\tif !%s {\n", isNil))
		_, _, typName := findStructName(t.X, pkgName)
		decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", valId, typName))
		maybeAddImport(typName, imports)
		generateDecodeForType(decodeBody, pkgName, t.X, valId, imports)
		decodeBody.WriteString(fmt.Sprintf("\t%s = &%s\n", fieldName, valId))
		decodeBody.WriteString("\t}\n")
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			listName := nextVar()
			_, _, typName := findStructName(t.Index, pkgName)
			decodeBody.WriteString(fmt.Sprintf("\tvar %s []%s\n", listName, typName))
			maybeAddImport(typName, imports)
			decodeArray(decodeBody, pkgName, t.Index, listName, imports)
			decodeBody.WriteString(fmt.Sprintf("\t%s = uniquelist.Make(%s)\n", fieldName, listName))
			imports[`"github.com/google/blueprint/uniquelist"`] = true
		} else if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "DepSet" {
			pkgName, typName, _ := findStructName(t.Index, pkgName)
			if typName == "string" {
				decodeBody.WriteString(fmt.Sprintf("\tif err = %s.DecodeString(buf); err != nil { return err }\n", fieldName))
			} else if findType(pkgName, typName, imports) == Interface {
				decodeBody.WriteString(fmt.Sprintf("\tif err = %s.DecodeInterface(buf); err != nil { return err }\n", fieldName))
			} else {
				decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(buf); err != nil { return err }\n", fieldName))
			}
		} else {
			decodeBody.WriteString(fmt.Sprintf("\tif err = %s.Decode(buf); err != nil { return err }\n", fieldName))
		}
	case *ast.SelectorExpr:
		pkgName, typName, _ := findStructName(t, pkgName)
		generateDecodeForCustomType(decodeBody, fieldName, pkgName, typName, imports)
	// anonymous struct
	case *ast.StructType:
		for _, f := range t.Fields.List {
			decodeBody.WriteString("\n")
			fName := fieldName + "."
			if len(f.Names) > 0 {
				fName += f.Names[0].Name
			}
			generateDecodeForType(decodeBody, pkgName, f.Type, fName, imports)
		}
	default:
		panic(fmt.Errorf("unknown data type: %v %T", t, t))
	}
}

func encodeArray(encodeBody *strings.Builder, pkgName string, t ast.Expr, fieldName string, imports map[string]bool) {
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(len(%s))); err != nil { return err }\n", fieldName))
	index := nextVar()
	encodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < len(%s); %s++ {\n", index, index, fieldName, index))
	generateEncodeForType(encodeBody, pkgName, t, fmt.Sprintf("%s[%s]", fieldName, index), imports)
	encodeBody.WriteString("\t}\n")
}

func decodeArray(decodeBody *strings.Builder, pkgName string, t ast.Expr, fieldName string, imports map[string]bool) {
	valId := nextVar()
	_, _, typName := findStructName(t, pkgName)
	decodeBody.WriteString(fmt.Sprintf("\tvar %s int32\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int32](buf, &%s); if err != nil { return err }\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\tif %s > 0 {\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\t%s = make([]%s, %s)\n", fieldName, typName, valId))
	maybeAddImport(typName, imports)
	index := nextVar()
	decodeBody.WriteString(fmt.Sprintf("\tfor %s := 0; %s < int(%s); %s++ {\n", index, index, valId, index))
	generateDecodeForType(decodeBody, pkgName, t, fmt.Sprintf("%s[%s]", fieldName, index), imports)
	decodeBody.WriteString("\t}\n")
	decodeBody.WriteString("\t}\n")
}

func generateEncode(pkgName string, structDecl *ast.TypeSpec, encodeBody *strings.Builder, imports map[string]bool) {
	structType, ok := structDecl.Type.(*ast.StructType)
	if !ok {
		return
	}
	structName := structDecl.Name.Name

	encodeBody.WriteString("func (r " + structName + ") GobEncode() ([]byte, error) {\n")
	encodeBody.WriteString("\tbuf := new(bytes.Buffer)\n\n")
	encodeBody.WriteString("\tif err := r.Encode(buf); err != nil { return nil, err }\n")
	encodeBody.WriteString("\n\treturn buf.Bytes(), nil\n")
	encodeBody.WriteString("}\n\n")

	encodeBody.WriteString("func (r " + structName + ") Encode(buf *bytes.Buffer) error {\n")
	encodeBody.WriteString("\tvar err error\n")

	for _, field := range structType.Fields.List {
		fieldName := "r."
		if len(field.Names) > 0 {
			fieldName += field.Names[0].Name
		}
		encodeBody.WriteString("\n")
		generateEncodeForType(encodeBody, pkgName, field.Type, fieldName, imports)
	}

	encodeBody.WriteString("\treturn err\n")
	encodeBody.WriteString("}\n")
}

func generateDecode(pkgName string, structDecl *ast.TypeSpec, decodeBody *strings.Builder, imports map[string]bool) {
	structType, ok := structDecl.Type.(*ast.StructType)
	if !ok {
		return
	}
	structName := structDecl.Name.Name

	decodeBody.WriteString("func (r *" + structName + ") GobDecode(b []byte) error {\n")
	decodeBody.WriteString("\tbuf := bytes.NewReader(b)\n")
	decodeBody.WriteString("\treturn r.Decode(buf)\n")
	decodeBody.WriteString("}\n\n")

	decodeBody.WriteString("func (r *" + structName + ") Decode(buf *bytes.Reader) error {\n")
	decodeBody.WriteString("\tvar err error\n")

	for _, field := range structType.Fields.List {
		fieldName := "r."
		if len(field.Names) > 0 {
			fieldName += field.Names[0].Name
		}
		decodeBody.WriteString("\n")
		generateDecodeForType(decodeBody, pkgName, field.Type, fieldName, imports)
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

	curDir, _ := os.Getwd()
	parts := strings.Split(curDir, blueprintPkgPath)
	if len(parts) < 2 {
		parts = strings.Split(curDir, soongPkgPath)
	}
	if len(parts) < 2 {
		// verify mode, the current directory is the base of the source tree.
		sourceDir = curDir
	} else {
		sourceDir = parts[0]
	}

	for _, s := range sources {
		out, err := generate(s)
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

func findPackagePath(pkgName string, imports map[string]bool) string {
	var pkgDir string
	baseDir := strings.TrimPrefix(importPkgs[pkgName], blueprintPkgPrefix)
	if baseDir != importPkgs[pkgName] {
		pkgDir = filepath.Join(sourceDir, blueprintPkgPath, baseDir)
	} else {
		baseDir = strings.TrimPrefix(importPkgs[pkgName], soongPkgPrefix)
		if baseDir != importPkgs[pkgName] {
			pkgDir = filepath.Join(sourceDir, soongPkgPath, baseDir)
		}
	}
	if pkgDir == "" {
		panic(fmt.Errorf("failed to find the package path: %s", importPkgs[pkgName]))
	}

	return pkgDir
}

func importPackage(pkgName string, pkgDir string) {
	if _, ok := pkgStructs[pkgName]; ok {
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
		loadTypes(node)
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

func generateRegistry(structDecl *ast.TypeSpec, codeBody *strings.Builder, initCodeBody *strings.Builder, imports map[string]bool) {
	if _, ok := structDecl.Type.(*ast.StructType); !ok {
		return
	}
	structName := structDecl.Name.Name
	typeId := structName + "GobRegId"
	codeBody.WriteString(fmt.Sprintf("\tvar %s int16\n", typeId))
	codeBody.WriteString("func (r " + structName + ") GetTypeId() int16 {\n")
	codeBody.WriteString(fmt.Sprintf("\treturn %s\n", typeId))
	codeBody.WriteString("}\n\n")

	initCodeBody.WriteString(fmt.Sprintf("\t%s = gobtools.RegisterType(func() gobtools.CustomDec { return new(%s) })\n", typeId, structName))
	imports[`"github.com/google/blueprint/gobtools"`] = true
}

func generate(source string) ([]byte, error) {
	node, err := parseFile(source) // Find the file containing the struct
	if err != nil {
		return nil, err
	}
	for _, p := range node.Imports {
		pkgPath := strings.ReplaceAll(p.Path.Value, "\"", "")
		if strings.HasPrefix(pkgPath, blueprintPkgPrefix) || strings.HasPrefix(pkgPath, soongPkgPrefix) {
			pkgName := path.Base(pkgPath)
			if _, ok := importPkgs[pkgName]; !ok {
				importPkgs[pkgName] = pkgPath
			}
		}
	}
	curPackage = node.Name.Name
	importPackage(curPackage, path.Dir(source))

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by go run gob_gen.go; DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n", curPackage)
	fmt.Fprintln(&b, "import (")

	var codeBodies []*strings.Builder
	imports := map[string]bool{}
	initCodeBody := &strings.Builder{}
	initCodeBody.WriteString("func init() {\n")
	structDecls := findStructs(node)
	for _, structDecl := range structDecls {
		fieldId = 0
		codeBody := &strings.Builder{}
		generateEncode(curPackage, structDecl, codeBody, imports)
		codeBody.WriteString("\n")
		fieldId = 0
		generateDecode(curPackage, structDecl, codeBody, imports)
		codeBodies = append(codeBodies, codeBody)
		generateRegistry(structDecl, codeBody, initCodeBody, imports)
	}

	if len(codeBodies) == 0 {
		return nil, nil
	}

	initCodeBody.WriteString("}\n\n")

	fmt.Fprintln(&b, strings.Join(slices.Sorted(maps.Keys(imports)), "\n"))
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
