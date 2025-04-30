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
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"os"
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

var sourceFile = flag.String("source", "", "source file")
var fieldId int

const genGobAnnotation = "@auto-generate: gob"

var imports = map[string]bool{
	"\"bytes\"": true,
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

func generateEncodeForType(encodeBody *strings.Builder, field ast.Expr, fieldName string) {
	fieldId++

	switch t := field.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeString(buf, %s); err != nil { return nil, err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		case "int":
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int64(%s)); err != nil { return nil, err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		case "bool", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			//typ := strings.ToUpper(string(t.Name[0])) + t.Name[1:]
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, %s); err != nil { return nil, err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		default:
			if fieldName == "" {
				fieldName = "r." + t.Name
			}
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeStruct(buf, &%s); err != nil { return nil, err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		}
	case *ast.MapType:
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(len(%s))); err != nil { return nil, err }\n", fieldName))
		encodeBody.WriteString(fmt.Sprintf("\tfor k, v := range %s {\n", fieldName))
		generateEncodeForType(encodeBody, t.Key, "k")
		generateEncodeForType(encodeBody, t.Value, "v")
		encodeBody.WriteString("\t}\n")
	case *ast.ArrayType:
		encodeArray(encodeBody, t.Elt, fieldName)
	case *ast.StarExpr:
		encodeBody.WriteString(fmt.Sprintf("\tisNil%d := %s == nil\n", fieldId, fieldName))
		encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, isNil%d); err != nil { return nil, err }\n", fieldId))
		encodeBody.WriteString(fmt.Sprintf("\tif !isNil%d {\n", fieldId))
		generateEncodeForType(encodeBody, t.X, "*"+fieldName)
		encodeBody.WriteString("\t}\n")
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			listName := fmt.Sprintf("unilst%d", fieldId)
			encodeBody.WriteString(fmt.Sprintf("\t%s := %s.ToSlice()\n", listName, fieldName))
			encodeArray(encodeBody, t.Index, listName)
		} else {
			encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeStruct(buf, &%s); err != nil { return nil, err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		}
	default:
		panic(fmt.Errorf("unknown data type: %v", t))
	}
}

func generateDecodeForType(decodeBody *strings.Builder, field ast.Expr, fieldName string) {
	fieldId++
	valId := fmt.Sprintf("val%d", fieldId)

	switch t := field.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeString(buf, &%s); if err != nil { return err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		case "int":
			decodeBody.WriteString(fmt.Sprintf("\tvar %s int64\n", valId))
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int64](buf, &%s); if err != nil { return err }\n", valId))
			decodeBody.WriteString(fmt.Sprintf("\t%s = int(%s)\n", fieldName, valId))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		case "bool", "int16", "int32", "int64", "uint16", "uint32", "uint64":
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[%s](buf, &%s); if err != nil { return err }\n", t.Name, fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		default:
			if fieldName == "" {
				fieldName = "r." + t.Name
			}
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeStruct(buf, &%s); if err != nil { return err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		}
	case *ast.MapType:
		kName := t.Key.(*ast.Ident).Name
		vName := t.Value.(*ast.Ident).Name
		decodeBody.WriteString(fmt.Sprintf("\tvar %s int32\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int32](buf, &%s); if err != nil { return err }\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\tif %s > 0 {\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\t%s = make(map[%s]%s, %s)\n", fieldName, kName, vName, valId))
		decodeBody.WriteString(fmt.Sprintf("\tfor i := 0; i < int(%s); i++ {\n", valId))
		decodeBody.WriteString(fmt.Sprintf("\tvar k %s\n", kName))
		decodeBody.WriteString(fmt.Sprintf("\tvar v %s\n", vName))
		generateDecodeForType(decodeBody, t.Key, "k")
		generateDecodeForType(decodeBody, t.Value, "v")
		decodeBody.WriteString(fmt.Sprintf("\t%s[k] = v\n", fieldName))
		decodeBody.WriteString("\t}\n")
		decodeBody.WriteString("\t}\n")
	case *ast.ArrayType:
		decodeArray(decodeBody, t.Elt, fieldName)
	case *ast.StarExpr:
		decodeBody.WriteString(fmt.Sprintf("\tvar isNil%d bool\n", fieldId))
		decodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.DecodeSimple(buf, &isNil%d); err != nil { return err }\n", fieldId))
		decodeBody.WriteString(fmt.Sprintf("\tif !isNil%d {\n", fieldId))
		decodeBody.WriteString(fmt.Sprintf("\tvar %s %s\n", valId, t.X.(*ast.Ident).Name))
		generateDecodeForType(decodeBody, t.X, valId)
		decodeBody.WriteString(fmt.Sprintf("\t%s = &%s\n", fieldName, valId))
		decodeBody.WriteString("\t}\n")
	case *ast.IndexExpr:
		if typ, ok := t.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "UniqueList" {
			listName := fmt.Sprintf("unilst%d", fieldId)
			decodeBody.WriteString(fmt.Sprintf("\tvar %s []%s\n", listName, t.Index.(*ast.Ident).Name))
			decodeArray(decodeBody, t.Index, listName)
			decodeBody.WriteString(fmt.Sprintf("\t%s = uniquelist.Make(%s)\n", fieldName, listName))
			imports["\"github.com/google/blueprint/uniquelist\""] = true
		} else {
			decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeStruct(buf, &%s); if err != nil { return err }\n", fieldName))
			imports["\"github.com/google/blueprint/gobtools\""] = true
		}
	default:
		panic(fmt.Errorf("unknown data type: %T", t))
	}
}

func encodeArray(encodeBody *strings.Builder, t ast.Expr, fieldName string) {
	encodeBody.WriteString(fmt.Sprintf("\tif err = gobtools.EncodeSimple(buf, int32(len(%s))); err != nil { return nil, err }\n", fieldName))
	encodeBody.WriteString(fmt.Sprintf("\tfor i := 0; i < len(%s); i++ {\n", fieldName))
	generateEncodeForType(encodeBody, t, fieldName+"[i]")
	encodeBody.WriteString("\t}\n")
}

func decodeArray(decodeBody *strings.Builder, t ast.Expr, fieldName string) {
	valId := fmt.Sprintf("val%d", fieldId)
	decodeBody.WriteString(fmt.Sprintf("\tvar %s int32\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\terr = gobtools.DecodeSimple[int32](buf, &%s); if err != nil { return err }\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\tif %s > 0 {\n", valId))
	decodeBody.WriteString(fmt.Sprintf("\t%s = make([]%s, %s)\n", fieldName, t.(*ast.Ident).Name, valId))
	decodeBody.WriteString(fmt.Sprintf("\tfor i := 0; i < int(%s); i++ {\n", valId))
	generateDecodeForType(decodeBody, t, fieldName+"[i]")
	decodeBody.WriteString("\t}\n")
	decodeBody.WriteString("\t}\n")
}

func generateEncode(structDecl *ast.TypeSpec, encodeBody *strings.Builder) {
	structType, ok := structDecl.Type.(*ast.StructType)
	if !ok {
		return
	}
	structName := structDecl.Name.Name

	encodeBody.WriteString("func (r " + structName + ") GobEncode() ([]byte, error) {\n")
	encodeBody.WriteString("\tbuf := new(bytes.Buffer)\n\n")
	encodeBody.WriteString("\tvar err error\n")

	for _, field := range structType.Fields.List {
		var fieldName string
		if len(field.Names) > 0 {
			fieldName = "r." + field.Names[0].Name
		}
		encodeBody.WriteString("\n")
		generateEncodeForType(encodeBody, field.Type, fieldName)
	}

	encodeBody.WriteString("\n\treturn buf.Bytes(), nil\n")
	encodeBody.WriteString("}\n")
}

func generateDecode(structDecl *ast.TypeSpec, decodeBody *strings.Builder) {
	structType, ok := structDecl.Type.(*ast.StructType)
	if !ok {
		return
	}
	structName := structDecl.Name.Name

	decodeBody.WriteString("func (r *" + structName + ") GobDecode(b []byte) error {\n")
	decodeBody.WriteString("\tbuf := bytes.NewReader(b)\n")
	decodeBody.WriteString("\terr := r.Decode(buf)\n")
	decodeBody.WriteString("\treturn err }\n\n")

	decodeBody.WriteString("func (r *" + structName + ") Decode(buf *bytes.Reader) error {\n")
	decodeBody.WriteString("\tvar err error\n")

	for _, field := range structType.Fields.List {
		var fieldName string
		if len(field.Names) > 0 {
			fieldName = "r." + field.Names[0].Name
		}
		decodeBody.WriteString("\n")
		generateDecodeForType(decodeBody, field.Type, fieldName)
	}

	decodeBody.WriteString("\n\treturn nil\n")
	decodeBody.WriteString("}\n")
}

func main() {
	flag.Parse()
	if flag.NArg() != 0 {
		panic("usage: gob_gen [--source source]")
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, *sourceFile, nil, parser.ParseComments) // Find the file containing the struct
	if err != nil {
		panic(err)
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by go run gob_gen.go -source %s; DO NOT EDIT.\n\n", *sourceFile)
	fmt.Fprintf(&b, "package %s\n", node.Name.Name)
	fmt.Fprintln(&b, "import (")

	var codeBodies []*strings.Builder
	structDecls := findStructs(node)
	for _, structDecl := range structDecls {
		fieldId = 0
		codeBody := &strings.Builder{}
		generateEncode(structDecl, codeBody)
		codeBody.WriteString("\n")
		generateDecode(structDecl, codeBody)
		codeBodies = append(codeBodies, codeBody)
	}

	fmt.Fprintln(&b, strings.Join(slices.Sorted(maps.Keys(imports)), "\n"))
	fmt.Fprintln(&b, ")")
	for _, codeBody := range codeBodies {
		fmt.Fprintf(&b, codeBody.String())
		fmt.Fprintln(&b)
	}

	source, err := format.Source(b.Bytes())
	if err != nil {
		panic(fmt.Errorf("source format error: %s", err))
	}
	output := strings.TrimSuffix(*sourceFile, ".go") + "_gob_enc.go"
	fd, err := os.Create(output)
	if err != nil {
		panic(err)
	}
	if _, err := fd.Write(source); err != nil {
		panic(err)
	}
	if err := fd.Close(); err != nil {
		panic(err)
	}
}
