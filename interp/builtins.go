// interp/builtins.go
package interp

import (
	"context"
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
	switch typ {
	case "int", "byte":
		return 0
	case "float64":
		return 0.0
	case "bool":
		return false
	case "string":
		return ""
	case "struct{}", "nil":
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
			data[i] = zeroValue(elem)
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
	if len(elems) > vm.maxContainerSize()-len(s.Data) {
		return nil, NewRuntimeError("append: size exceeds interpreter limit")
	}
	if s.ElementType == "byte" {
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
	case "float64":
		return ToFloat(v)
	case "bool":
		return ToBool(v)
	case "string":
		return ToString(v)
	case "byte":
		return ToInt(v) & 0xFF
	default:
		return v
	}
}

func isBuiltinType(name string) bool {
	switch name {
	case "int", "float64", "bool", "string", "byte":
		return true
	default:
		return false
	}
}
