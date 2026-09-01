package interp

import (
	"go/ast"
	"strconv"
)

// astStructTag turns the parser's quoted literal into the tag text exposed by
// reflection. Invalid literals are retained verbatim so parsing tags never
// changes the behavior of an otherwise accepted guest program.
func astStructTag(literal *ast.BasicLit) string {
	if literal == nil {
		return ""
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return literal.Value
	}
	return value
}
