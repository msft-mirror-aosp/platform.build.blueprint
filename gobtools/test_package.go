package gobtools

type TypeAlias = []TypeStruct

type TypeIdent TypeStruct

type TypeInterface interface {
	print(value string) string
}

type TypeStruct struct {
	Name string
}

func (t TypeStruct) print(value string) string {
	return value + t.Name
}
