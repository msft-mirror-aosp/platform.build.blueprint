package test

//go:generate go run ../codegen/gob_gen.go

type TypeAlias = []TypeStruct

type TypeIdent TypeStruct

type TypeBasic int

type TypeInterface interface {
	print(value string) string
}

// @auto-generate: gob
type TypeStruct struct {
	Name string
}

func (t TypeStruct) print(value string) string {
	return value + t.Name
}
