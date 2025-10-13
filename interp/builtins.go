// interp/builtins.go
package interp

import (
	"go/ast"
	"strings"
)

// typeString builds a textual type for simple types used by nanoGo.
func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		// We only support slices (Len == nil). Fixed arrays are not yet supported.
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.ChanType:
		// Direction is ignored for runtime dynamics.
		return "chan " + typeString(t.Value)
	case *ast.SelectorExpr:
		// No full package typing; reduce to identifier (e.g., sync.WaitGroup -> WaitGroup)
		return typeString(t.Sel)
	}
	return ""
}

// parseMapType splits "map[Key]Val" into key and value type strings.
func parseMapType(s string) (key, val string) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "map[") { return "", "" }
	i := 4 // after "map["
	depth := 1; start := i
	for i < len(s) {
		switch s[i] {
		case '[': depth++
		case ']':
			depth--
			if depth == 0 {
				key = strings.TrimSpace(s[start:i])
				if i+1 < len(s) { val = strings.TrimSpace(s[i+1:]) }
				return
			}
		}
		i++
	}
	return "", ""
}

func zeroValue(typ string) any {
	switch typ {
	case "int", "byte": return 0
	case "float64": return 0.0
	case "bool": return false
	case "string": return ""
	case "struct{}", "nil": return nil
	default:
		if strings.HasPrefix(typ, "*") {
			return (*StructVal)(nil)
		}
		if strings.HasPrefix(typ, "[]") {
			return &SliceVal{ElementType: typ[2:], Data: []any{}}
		}
		if strings.HasPrefix(typ, "map[") {
			k, v := parseMapType(typ)
			return &MapVal{KeyType: k, ElementType: v, Data: map[string]any{}, Keys: map[string]any{}}
		}
		if strings.HasPrefix(typ, "chan ") {
			return &ChannelVal{ElementType: typ[5:], C: make(chan any), Closed: false}
		}
		return &StructVal{TypeName: typ, Fields: map[string]any{}}
	}
}

// --------------- Builtins -----------------------

func builtinMake(typ string, args []any) any {
	// Slices: make([]T, len[, cap])
	if strings.HasPrefix(typ, "[]") {
		elem := typ[2:]
		length := 0; capacity := 0
		if len(args) >= 1 { length = ToInt(args[0]) }
		if len(args) >= 2 { capacity = ToInt(args[1]) }
		if capacity < length { capacity = length }
		data := make([]any, length, capacity)
		for i := 0; i < length; i++ { data[i] = zeroValue(elem) }
		return &SliceVal{ElementType: elem, Data: data}
	}
	// Maps: make(map[K]V)
	if strings.HasPrefix(typ, "map[") {
		k, v := parseMapType(typ)
		return &MapVal{KeyType: k, ElementType: v, Data: map[string]any{}, Keys: map[string]any{}}
	}
	// Channels: make(chan T[, cap])
	if strings.HasPrefix(typ, "chan ") {
		elem := strings.TrimSpace(typ[5:])
		cap := 0
		if len(args) >= 1 { cap = ToInt(args[0]) }
		if cap < 0 { cap = 0 }
		if cap == 0 {
			return &ChannelVal{ElementType: elem, C: make(chan any), Closed: false}
		}
		return &ChannelVal{ElementType: elem, C: make(chan any, cap), Closed: false}
	}
	return nil
}

func builtinLen(v any) int {
	switch x := v.(type) {
	case string: return len(x)
	case *SliceVal: return len(x.Data)
	case *MapVal: return len(x.Data)
	default: return 0
	}
}

func builtinCap(v any) int {
	switch x := v.(type) {
	case *SliceVal: return cap(x.Data)
	default: return 0
	}
}

func builtinAppend(slice any, elems ...any) any {
	s, ok := slice.(*SliceVal); if !ok { return slice }
	for _, e := range elems {
		if s.ElementType == "byte" { s.Data = append(s.Data, ToInt(e)&0xFF) } else { s.Data = append(s.Data, e) }
	}
	return s
}

func builtinCopy(dst any, src any) int {
	d, ok1 := dst.(*SliceVal); s, ok2 := src.(*SliceVal)
	if !ok1 || !ok2 { return 0 }
	n := len(s.Data); if len(d.Data) < n { n = len(d.Data) }
	for i := 0; i < n; i++ { d.Data[i] = s.Data[i] }
	return n
}

func builtinClose(ch any) any {
	c, ok := ch.(*ChannelVal); if !ok || c == nil { return nil }
	if !c.Closed { close(c.C); c.Closed = true }
	return nil
}

// Simple type conversion calls: string([]byte), float64(int), etc.
func builtinConvert(typ string, v any) any {
	switch typ {
	case "int": return ToInt(v)
	case "float64": return ToFloat(v)
	case "bool": return ToBool(v)
	case "string": return ToString(v)
	case "byte": return ToInt(v) & 0xFF
	default: return v
	}
}

func isBuiltinType(name string) bool {
	switch name {
	case "int", "float64", "bool", "string", "byte":
		return true
	default:	return false
	}
}
