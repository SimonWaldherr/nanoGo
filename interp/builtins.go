// interp/builtins.go
package interp

import (
	"context"
	"go/ast"
	"strconv"
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
		if t.Len != nil {
			if lit, ok := t.Len.(*ast.BasicLit); ok {
				return "[" + lit.Value + "]" + typeString(t.Elt)
			}
			// Preserve the type shape even when its constant length cannot be
			// resolved without the evaluator's lexical environment.
			return "[?]" + typeString(t.Elt)
		}
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
	if !strings.HasPrefix(s, "map[") {
		return "", ""
	}
	i := 4 // after "map["
	depth := 1
	start := i
	for i < len(s) {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				key = strings.TrimSpace(s[start:i])
				if i+1 < len(s) {
					val = strings.TrimSpace(s[i+1:])
				}
				return
			}
		}
		i++
	}
	return "", ""
}

func zeroValue(typ string) any {
	if length, elem, ok := parseArrayType(typ); ok {
		data := make([]any, length)
		for i := range data {
			data[i] = zeroValue(elem)
		}
		return &SliceVal{ElementType: elem, Data: data, Fixed: true}
	}
	switch typ {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
		return 0
	case "float32", "float64":
		return 0.0
	case "bool":
		return false
	case "string":
		return ""
	case "struct{}", "nil":
		return nil
	case "any", "interface{}", "error":
		// The zero value of any interface type — including the predeclared
		// `any` and `error` — is nil, never a struct. Without this case
		// these fell through to the struct fallback below (treating the
		// unrecognized name "any"/"error" as if it were a user struct
		// type), which meant e.g. `var err error` started out as a
		// non-nil placeholder instead of nil.
		return nil
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

// zeroValueForType resolves scalar named types such as `type Pin uint8`.
// Their dynamic value is the underlying scalar, while the source retains the
// named type for declarations and conversions.
func (vm *Interpreter) zeroValueForType(typ string) any {
	if td := vm.types[typ]; td != nil {
		if td.Kind == "alias" {
			return zeroValue(td.Underlying)
		}
		if td.Kind == "interface" {
			// A named interface's zero value is nil, same as "any"/"error"
			// below — there is no implementation to default-construct.
			return nil
		}
	}
	return zeroValue(typ)
}

func (vm *Interpreter) coerceToType(val any, typ string) any {
	if td := vm.types[typ]; td != nil && td.Kind == "alias" {
		typ = td.Underlying
	}
	return coerceToType(val, typ)
}

// parseArrayType recognizes the concrete fixed-array spelling emitted by
// typeString, e.g. "[32]byte". Variable-sized forms stay unsupported rather
// than guessing their size at runtime.
func parseArrayType(typ string) (length int, elem string, ok bool) {
	if !strings.HasPrefix(typ, "[") || strings.HasPrefix(typ, "[]") {
		return 0, "", false
	}
	end := strings.IndexByte(typ, ']')
	if end <= 1 || end == len(typ)-1 {
		return 0, "", false
	}
	n, err := strconv.Atoi(typ[1:end])
	if err != nil || n < 0 {
		return 0, "", false
	}
	return n, typ[end+1:], true
}

// coerceToType widens an untyped int literal's evaluated Go value to a
// declared float64 type, mirroring Go's implicit conversion of an untyped
// int constant to a float64 variable or struct field (e.g. `var x float64 =
// 3` or `Rect{W: 3}` with `W float64`). nanoGo has no static type checker,
// so without this a declared-float64 binding stays a plain Go int whenever
// it is only ever initialized with integer-looking literals, silently
// turning later arithmetic (and any /-division on it) back into int math.
// Every other combination is returned unchanged.
func coerceToType(val any, typ string) any {
	if isBuiltinType(typ) {
		return builtinConvert(typ, val)
	}
	return val
}

// --------------- Builtins -----------------------

func (vm *Interpreter) builtinMake(typ string, args []any) (any, error) {
	// Slices: make([]T, len[, cap])
	if strings.HasPrefix(typ, "[]") {
		elem := typ[2:]
		length := 0
		capacity := 0
		if len(args) >= 1 {
			length = ToInt(args[0])
		}
		if len(args) >= 2 {
			capacity = ToInt(args[1])
		}
		if length < 0 || capacity < 0 {
			return nil, NewRuntimeError("make: negative size")
		}
		if capacity < length {
			capacity = length
		}
		if capacity > vm.maxContainerSize() {
			return nil, NewRuntimeError("make: size exceeds interpreter limit")
		}
		data := make([]any, length, capacity)
		for i := 0; i < length; i++ {
			data[i] = vm.zeroValueForType(elem)
		}
		return &SliceVal{ElementType: elem, Data: data}, nil
	}
	// Maps: make(map[K]V)
	if strings.HasPrefix(typ, "map[") {
		if len(args) >= 1 && (ToInt(args[0]) < 0 || ToInt(args[0]) > vm.maxContainerSize()) {
			return nil, NewRuntimeError("make: size exceeds interpreter limit")
		}
		k, v := parseMapType(typ)
		return &MapVal{KeyType: k, ElementType: v, Data: map[string]any{}, Keys: map[string]any{}}, nil
	}
	// Channels: make(chan T[, cap])
	if strings.HasPrefix(typ, "chan ") {
		elem := strings.TrimSpace(typ[5:])
		cap := 0
		if len(args) >= 1 {
			cap = ToInt(args[0])
		}
		if cap < 0 {
			return nil, NewRuntimeError("make: negative size")
		}
		if cap > vm.maxContainerSize() {
			return nil, NewRuntimeError("make: size exceeds interpreter limit")
		}
		if cap == 0 {
			return &ChannelVal{ElementType: elem, C: make(chan any), Closed: false}, nil
		}
		return &ChannelVal{ElementType: elem, C: make(chan any, cap), Closed: false}, nil
	}
	return nil, NewRuntimeError("make: unsupported type")
}

func builtinLen(v any) int {
	switch x := v.(type) {
	case string:
		return len(x)
	case *SliceVal:
		return len(x.Data)
	case *MapVal:
		return len(x.Data)
	default:
		return 0
	}
}

func builtinCap(v any) int {
	switch x := v.(type) {
	case *SliceVal:
		return cap(x.Data)
	default:
		return 0
	}
}

func (vm *Interpreter) builtinAppend(slice any, elems ...any) (any, error) {
	s, ok := slice.(*SliceVal)
	if !ok {
		return slice, nil
	}
	if s.Fixed {
		return nil, NewRuntimeError("append: first argument must be a slice")
	}
	if len(elems) > vm.maxContainerSize()-len(s.Data) {
		return nil, NewRuntimeError("append: size exceeds interpreter limit")
	}
	if isByteType(s.ElementType) {
		for _, e := range elems {
			s.Data = append(s.Data, ToInt(e)&0xFF)
		}
	} else {
		// A single batched append lets the runtime compute the required
		// capacity once, instead of potentially growing/copying the
		// backing array once per element for large multi-value or
		// spread (append(dst, src...)) calls.
		s.Data = append(s.Data, elems...)
	}
	return s, nil
}

func isByteType(typ string) bool {
	return typ == "byte" || typ == "uint8"
}

// builtinCopy mirrors Go's own copy(): SliceVal.Data values can alias the
// same backing array (slicing shares it, see the SliceExpr case in
// evaluator.go), so a forward-only element-by-element loop corrupts
// overlapping copies (e.g. the "shift right to insert" idiom
// copy(s[i+1:], s[i:])); Go's copy() is overlap-safe (memmove-based) and
// also faster for the common non-overlapping case.
func builtinCopy(dst any, src any) int {
	d, ok1 := dst.(*SliceVal)
	s, ok2 := src.(*SliceVal)
	if !ok1 || !ok2 {
		return 0
	}
	return copy(d.Data, s.Data)
}

func builtinClose(ch any) (any, error) {
	c, ok := ch.(*ChannelVal)
	if !ok || c == nil {
		return nil, NewRuntimeError("close of non-channel")
	}
	return nil, c.Close()
}

// Send and Receive make guest channel operations context-aware. Close races
// are translated to runtime errors instead of letting a guest panic the host.
func (c *ChannelVal) Send(ctx context.Context, value any) (err error) {
	if c == nil {
		return NewRuntimeError("send on nil channel")
	}
	if c.direction == channelReceiveOnly {
		return NewRuntimeError("send on receive-only host channel")
	}
	c.mu.RLock()
	closed := c.Closed
	done := c.done
	c.mu.RUnlock()
	if closed {
		return NewRuntimeError("send on closed channel")
	}
	defer func() {
		if recover() != nil {
			err = NewRuntimeError("send on closed channel")
		}
	}()
	if done == nil {
		select {
		case c.C <- value:
			return nil
		case <-ctx.Done():
			return contextError(ctx)
		}
	}
	select {
	case c.C <- value:
		return nil
	case <-done:
		return NewRuntimeError("send on closed channel")
	case <-ctx.Done():
		return contextError(ctx)
	}
}

func (c *ChannelVal) Receive(ctx context.Context) (value any, open bool, err error) {
	if c == nil {
		return nil, false, NewRuntimeError("receive on nil channel")
	}
	if c.direction == channelSendOnly {
		return nil, false, NewRuntimeError("receive on send-only host channel")
	}
	c.mu.RLock()
	done := c.done
	c.mu.RUnlock()
	if done == nil {
		select {
		case value, open = <-c.C:
			return value, open, nil
		case <-ctx.Done():
			return nil, false, contextError(ctx)
		}
	}
	select {
	case value = <-c.C:
		return value, true, nil
	case <-done:
		// Preserve buffered values sent before host input was closed.
		select {
		case value = <-c.C:
			return value, true, nil
		default:
			return nil, false, nil
		}
	case <-ctx.Done():
		return nil, false, contextError(ctx)
	}
}

func (c *ChannelVal) Close() error {
	if c == nil {
		return NewRuntimeError("close of nil channel")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hostOwned {
		return NewRuntimeError("cannot close host-owned channel")
	}
	if c.Closed {
		return NewRuntimeError("close of closed channel")
	}
	c.Closed = true
	close(c.C)
	return nil
}

// TrySend is the non-blocking counterpart used by timers. It protects the
// host timer goroutine from a guest closing the timer channel concurrently.
func (c *ChannelVal) TrySend(value any) (sent bool, err error) {
	if c == nil {
		return false, NewRuntimeError("send on nil channel")
	}
	c.mu.RLock()
	closed := c.Closed
	c.mu.RUnlock()
	if closed {
		return false, NewRuntimeError("send on closed channel")
	}
	defer func() {
		if recover() != nil {
			sent = false
			err = NewRuntimeError("send on closed channel")
		}
	}()
	select {
	case c.C <- value:
		return true, nil
	default:
		return false, nil
	}
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}

// Simple type conversion calls: string([]byte), float64(int), etc.
func builtinConvert(typ string, v any) any {
	switch typ {
	case "int":
		return ToInt(v)
	case "int8":
		return int(int8(ToInt(v)))
	case "int16":
		return int(int16(ToInt(v)))
	case "int32", "rune":
		return int(int32(ToInt(v)))
	case "int64":
		return ToInt(v)
	case "uint":
		return int(uint(ToInt(v)))
	case "uint8", "byte":
		return ToInt(v) & 0xFF
	case "uint16":
		return ToInt(v) & 0xFFFF
	case "uint32":
		return int(uint32(ToInt(v)))
	case "uint64", "uintptr":
		return ToInt(v)
	case "float32":
		return float64(float32(ToFloat(v)))
	case "float64":
		return ToFloat(v)
	case "bool":
		return ToBool(v)
	case "string":
		return ToString(v)
	default:
		return v
	}
}

func isBuiltinType(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64", "bool", "string", "byte", "rune":
		return true
	default:
		return false
	}
}
