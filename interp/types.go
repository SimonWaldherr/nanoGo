// interp/types.go
package interp

import (
	"fmt"
	"strings"
)

// RuntimeError is a lightweight error type for runtime faults.
type RuntimeError struct{ msg string }
func (e *RuntimeError) Error() string  { return e.msg }
func NewRuntimeError(msg string) error { return &RuntimeError{msg: msg} }

// panicError is used internally to model Go's panic unwinding.
type panicError struct{ value any }
func (e *panicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// FieldDef/TypeDef describe simple struct types (name, fields, methods).
type FieldDef struct{ Name, Type string }

type TypeDef struct {
	Name    string
	Kind    string // "struct", "interface", "chan"
	Fields  []FieldDef
	Methods map[string]*Function
}

// Function represents either a user-defined or native function.
type Function struct {
	Name          string
	Params        []string
	IsVariadic    bool
	Body          any // *ast.BlockStmt for user functions
	Env           *Env
	Native        func(args []any) (any, error)

	RecvName      string // method receiver var name
	RecvType      string // method receiver type (without "*")
}

// StructVal, SliceVal, MapVal, ChannelVal are dynamic runtime containers.
type StructVal struct {
	TypeName string
	Fields   map[string]any
}

type SliceVal struct {
	ElementType string
	Data        []any
}

type MapVal struct {
	KeyType, ElementType string
	Data map[string]any  // hashed key -> value
	Keys map[string]any  // hashed key -> original key
}

func (m *MapVal) getByKey(k any) (any, bool) {
	h := hashKey(k); v, ok := m.Data[h]; return v, ok
}
func (m *MapVal) setByKey(k, v any) { h := hashKey(k); m.Data[h] = v; m.Keys[h] = k }
func (m *MapVal) deleteByKey(k any) { h := hashKey(k); delete(m.Data, h); delete(m.Keys, h) }

// ChannelVal models a typed channel with a Go channel underneath.
type ChannelVal struct {
	ElementType string
	C           chan any
	Closed      bool
}

func hashKey(v any) string {
	switch t := v.(type) {
	case int:		return fmt.Sprintf("i:%d", t)
	case int64:		return fmt.Sprintf("I:%d", t)
	case string:	return "s:" + t
	case bool:		if t { return "b:1" } ; return "b:0"
	case *StructVal:
		var b strings.Builder
		b.WriteString("struct:"); b.WriteString(t.TypeName); b.WriteByte(':')
		names := make([]string, 0, len(t.Fields))
		for k := range t.Fields { names = append(names, k) }
		for i := 0; i < len(names); i++ { for j := i+1; j < len(names); j++ { if names[j] < names[i] { names[i], names[j] = names[j], names[i] } } }
		for _, name := range names { b.WriteString(name); b.WriteByte('='); b.WriteString(hashKey(t.Fields[name])); b.WriteByte(';') }
		return b.String()
	default:	return fmt.Sprintf("u:%T:%v", v, v)
	}
}

// Conversions (runtime-dynamic, intentionally permissive for this subset) ----

func ToInt(v any) int {
	switch x := v.(type) {
	case int:	return x
	case int64:	return int(x)
	case float64:	return int(x)
	case bool:	if x { return 1 } ; return 0
	case string:
		n := 0; s := x; sign := 1
		if len(s) > 0 && (s[0] == '+' || s[0] == '-') { if s[0] == '-' { sign = -1 }; s = s[1:] }
		for i := 0; i < len(s); i++ { c := s[i]; if c < '0' || c > '9' { break }; n = n*10 + int(c-'0') }
		return sign*n
	}
	return 0
}

func ToFloat(v any) float64 {
	switch x := v.(type) {
	case float64:	return x
	case int:		return float64(x)
	case int64:		return float64(x)
	case bool:		if x { return 1 } ; return 0
	case string:
		s := x; sign := 1.0; i := 0
		if len(s) > 0 && (s[0] == '+' || s[0] == '-') { if s[0] == '-' { sign = -1 }; i++ }
		intp := 0.0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ { intp = intp*10 + float64(s[i]-'0') }
		frac := 0.0; base := 1.0
		if i < len(s) && s[i] == '.' { i++; for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ { frac = frac*10 + float64(s[i]-'0'); base *= 10 } }
		return sign * (intp + frac/base)
	}
	return 0
}

func ToBool(v any) bool {
	switch x := v.(type) {
	case bool:	return x
	case int:	return x != 0
	case int64:	return x != 0
	case float64:	return x != 0
	case string:	return x != "" && x != "0" && x != "false"
	}
	return v != nil
}

func ToString(v any) string {
	switch x := v.(type) {
	case string:	return x
	case int:		return fmt.Sprintf("%d", x)
	case int64:		return fmt.Sprintf("%d", x)
	case float64:	return fmt.Sprintf("%g", x)
	case bool:		if x { return "true" } ; return "false"
	case *SliceVal:
		if x.ElementType == "byte" {
			b := make([]byte, len(x.Data))
			for i := range b { b[i] = byte(ToInt(x.Data[i]) & 0xFF) }
			return string(b)
		}
	}
	return fmt.Sprintf("%v", v)
}

func IsZero(v any) bool {
	switch x := v.(type) {
	case nil:		return true
	case int:		return x == 0
	case float64:	return x == 0
	case bool:		return !x
	case string:	return x == ""
	case *SliceVal:	return len(x.Data) == 0
	case *MapVal:	return len(x.Data) == 0
	case *StructVal:	return false
	case *ChannelVal:	return x == nil
	}
	return v == nil
}
