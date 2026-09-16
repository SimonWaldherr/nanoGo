package interp

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"strings"
)

// JSON has a bounded conversion envelope independently of execution steps:
// native encoding/json does not execute guest checkpoints while decoding.
const maxJSONDepth = 128
const maxJSONBytes = 16 << 20
const maxJSONValues = 1 << 20

func (vm *Interpreter) jsonStopError() error {
	if e := vm.activeExecution; e != nil {
		if err := e.err(); err != nil {
			return err
		}
		if e.ctx.Err() != nil {
			return vm.cancellationError()
		}
	}
	return nil
}

func registerJSONPackage(vm *Interpreter) {
	pkg := &Package{Name: "encoding/json", Funcs: map[string]*Function{}}
	pkg.Funcs["Marshal"] = &Function{Name: "Marshal", Params: []string{"value"}, Native: func(args []any) (any, error) {
		if len(args) != 1 {
			return nil, strictHostFailure(NewRuntimeError("json.Marshal expects 1 argument"))
		}
		data, err := vm.marshalJSON(args[0])
		if stop := vm.jsonStopError(); stop != nil {
			return nil, strictHostFailure(stop)
		}
		if err != nil {
			return ReturnValues{&SliceVal{ElementType: "byte"}, err}, nil
		}
		if err := vm.chargeAllocation(uint64(len(data))); err != nil {
			return nil, strictHostFailure(err)
		}
		return ReturnValues{byteSliceValue(data), nil}, nil
	}}
	pkg.Funcs["Unmarshal"] = &Function{Name: "Unmarshal", Params: []string{"data", "target"}, Native: func(args []any) (any, error) {
		if len(args) != 2 {
			return nil, strictHostFailure(NewRuntimeError("json.Unmarshal expects 2 arguments"))
		}
		data, err := jsonByteArgument(args[0])
		if err != nil {
			return ReturnValues{err}, nil
		}
		err = vm.unmarshalJSON(data, args[1])
		if stop := vm.jsonStopError(); stop != nil {
			return nil, strictHostFailure(stop)
		}
		return ReturnValues{err}, nil
	}}
	vm.RegisterPackage("encoding/json", pkg)
	vm.RegisterPackage("json", pkg)
}

// The legacy facade has its own import path so arity/assignment context never
// selects semantics. Its historical native-error behavior is retained.
func registerJSONLegacyPackage(vm *Interpreter) {
	pkg := &Package{Name: "jsonlegacy", Funcs: map[string]*Function{}}
	pkg.Funcs["Marshal"] = &Function{Name: "Marshal", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "null", nil
		}
		data, err := vm.marshalJSON(args[0])
		return string(data), err
	}}
	pkg.Funcs["Unmarshal"] = &Function{Name: "Unmarshal", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, nil
		}
		data := []byte(ToString(args[0]))
		if err := validateJSONEnvelope(data); err != nil {
			return nil, err
		}
		var value any
		err := json.Unmarshal(data, &value)
		return value, err
	}}
	vm.RegisterPackage("nanogo/jsonlegacy", pkg)
}

func jsonByteArgument(value any) ([]byte, error) {
	value = unwrapNamedValue(value)
	if b, ok := value.([]byte); ok {
		if len(b) > maxJSONBytes {
			return nil, fmt.Errorf("json: payload exceeds %d bytes", maxJSONBytes)
		}
		return b, nil
	}
	s, ok := value.(*SliceVal)
	if !ok || s == nil || s.Fixed || !isByteType(s.ElementType) {
		return nil, fmt.Errorf("json.Unmarshal: data must be []byte")
	}
	if len(s.Data) > maxJSONBytes {
		return nil, fmt.Errorf("json: payload exceeds %d bytes", maxJSONBytes)
	}
	data := make([]byte, len(s.Data))
	for i, v := range s.Data {
		n, ok := v.(int)
		if !ok || n < 0 || n > 255 {
			return nil, fmt.Errorf("json: invalid byte at index %d", i)
		}
		data[i] = byte(n)
	}
	return data, nil
}

// Check depth before the standard decoder creates any nested containers.
// Syntax, escapes and delimiters are still validated by encoding/json.
func validateJSONEnvelope(data []byte) error {
	if len(data) > maxJSONBytes {
		return fmt.Errorf("json: payload exceeds %d bytes", maxJSONBytes)
	}
	depth := 0
	quoted := false
	escape := false
	values := 0
	for _, c := range data {
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			values++
			if depth > maxJSONDepth {
				return fmt.Errorf("json: nesting exceeds %d levels", maxJSONDepth)
			}
		case '}', ']':
			depth--
		case ',':
			values++
		}
		if values > maxJSONValues {
			return fmt.Errorf("json: value count exceeds %d", maxJSONValues)
		}
	}
	return nil
}

type jsonConversion struct {
	vm              *Interpreter
	types           map[string]reflect.Type
	building        map[string]bool
	active          map[any]bool
	pointers        map[uintptr]*PointerVal
	pointerValues   map[string]reflect.Value
	maps            map[*MapVal]reflect.Value
	slices          map[*SliceVal]reflect.Value
	preserveAliases bool
	values, bytes   int
}

func newJSONConversion(vm *Interpreter) *jsonConversion {
	return &jsonConversion{vm: vm}
}

func (c *jsonConversion) resolve(typ string) string {
	typ = strings.ReplaceAll(typ, "interface {}", "any")
	for i := 0; i < maxJSONDepth; i++ {
		td := c.vm.types[typ]
		if td == nil || (td.Kind != "alias" && td.Kind != "named") {
			return typ
		}
		typ = td.Underlying
	}
	return typ
}

func (c *jsonConversion) typ(name string, depth int) (reflect.Type, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("json: type nesting exceeds %d levels", maxJSONDepth)
	}
	if td := c.vm.types[name]; td != nil && (td.Methods["MarshalJSON"] != nil || td.Methods["UnmarshalJSON"] != nil) {
		return nil, fmt.Errorf("json: custom JSON methods on %s are unsupported", name)
	}
	name = c.resolve(name)
	if typ := c.types[name]; typ != nil {
		return typ, nil
	}
	var t reflect.Type
	switch name {
	case "any", "interface{}", "<nil>", "nil":
		return reflect.TypeFor[any](), nil
	case "bool":
		return reflect.TypeFor[bool](), nil
	case "string":
		return reflect.TypeFor[string](), nil
	case "int":
		return reflect.TypeFor[int](), nil
	case "int8":
		return reflect.TypeFor[int8](), nil
	case "int16":
		return reflect.TypeFor[int16](), nil
	case "int32", "rune":
		return reflect.TypeFor[int32](), nil
	case "int64":
		return reflect.TypeFor[int64](), nil
	case "uint":
		return reflect.TypeFor[uint](), nil
	case "uint8", "byte":
		return reflect.TypeFor[byte](), nil
	case "uint16":
		return reflect.TypeFor[uint16](), nil
	case "uint32":
		return reflect.TypeFor[uint32](), nil
	case "uint64":
		return reflect.TypeFor[uint64](), nil
	case "uintptr":
		return reflect.TypeFor[uintptr](), nil
	case "float32":
		return reflect.TypeFor[float32](), nil
	case "float64":
		return reflect.TypeFor[float64](), nil
	default:
		if c.building[name] {
			return nil, fmt.Errorf("json: recursive type %s is unsupported", name)
		}
		if c.building == nil {
			c.building = make(map[string]bool)
		}
		c.building[name] = true
		defer delete(c.building, name)
		if strings.HasPrefix(name, "*") {
			e, err := c.typ(name[1:], depth+1)
			if err != nil {
				return nil, err
			}
			t = reflect.PointerTo(e)
		} else if strings.HasPrefix(name, "[]") {
			e, err := c.typ(name[2:], depth+1)
			if err != nil {
				return nil, err
			}
			t = reflect.SliceOf(e)
		} else if n, e, ok := parseArrayType(name); ok {
			if n > maxJSONValues {
				return nil, fmt.Errorf("json: array too large")
			}
			et, err := c.typ(e, depth+1)
			if err != nil {
				return nil, err
			}
			if n > 0 && et.Size() > maxJSONBytes/uintptr(n) {
				return nil, fmt.Errorf("json: array representation exceeds %d bytes", maxJSONBytes)
			}
			t = reflect.ArrayOf(n, et)
		} else if strings.HasPrefix(name, "map[") {
			k, e := parseMapType(name)
			kt, err := c.typ(k, depth+1)
			if err != nil {
				return nil, err
			}
			if kt.Kind() != reflect.String && (kt.Kind() < reflect.Int || kt.Kind() > reflect.Uintptr) {
				return nil, fmt.Errorf("json: unsupported map key type %s", k)
			}
			et, err := c.typ(e, depth+1)
			if err != nil {
				return nil, err
			}
			t = reflect.MapOf(kt, et)
		} else if td := c.vm.types[name]; td != nil && td.Kind == "struct" {
			if td.hasEmbeddedFields {
				return nil, fmt.Errorf("json: embedded fields on %s are unsupported", name)
			}
			if td.Methods["MarshalJSON"] != nil || td.Methods["UnmarshalJSON"] != nil {
				return nil, fmt.Errorf("json: custom JSON methods on %s are unsupported", name)
			}
			fields := make([]reflect.StructField, 0, len(td.Fields))
			seen := make(map[string]bool, len(td.Fields))
			var storage uintptr
			for _, f := range td.Fields {
				if !token.IsIdentifier(f.Name) || (f.Name != "_" && seen[f.Name]) {
					return nil, fmt.Errorf("json: invalid or duplicate field %q", f.Name)
				}
				seen[f.Name] = true
				if !ast.IsExported(f.Name) {
					continue
				}
				_, _, skip := jsonFieldOptions(f.Name, f.Tag)
				if skip {
					continue
				}
				ft, err := c.typ(f.Type, depth+1)
				if err != nil {
					return nil, err
				}
				cost := ft.Size() + uintptr(ft.Align())
				if cost > maxJSONBytes-storage {
					return nil, fmt.Errorf("json: struct representation exceeds %d bytes", maxJSONBytes)
				}
				storage += cost
				fields = append(fields, reflect.StructField{Name: f.Name, Type: ft, Tag: reflect.StructTag(f.Tag)})
			}
			t = reflect.StructOf(fields)
		} else {
			return nil, fmt.Errorf("json: unsupported type %s", name)
		}
	}
	if c.types == nil {
		c.types = make(map[string]reflect.Type)
	}
	c.types[name] = t
	return t, nil
}

func (c *jsonConversion) value(value any, t reflect.Type, depth int) (reflect.Value, error) {
	zero := reflect.Zero(t)
	c.values++
	if c.values&255 == 0 {
		if err := c.vm.jsonStopError(); err != nil {
			return zero, err
		}
	}
	if depth > maxJSONDepth || c.values > maxJSONValues {
		return zero, fmt.Errorf("json: value nesting or count limit exceeded")
	}
	if value == nil {
		return zero, nil
	}
	if t.Kind() != reflect.Interface {
		value = unwrapNamedValue(value)
	}
	var identity any
	switch v := value.(type) {
	case *PointerVal:
		identity = v
	case *StructVal:
		identity = v
	case *MapVal:
		identity = v
	case *SliceVal:
		identity = v
	case string:
		c.bytes += len(v)
		if c.bytes > maxJSONBytes {
			return zero, fmt.Errorf("json: string data exceeds %d bytes", maxJSONBytes)
		}
	}
	// Interfaces introduce no new value; unwrap them before marking the node.
	if t.Kind() == reflect.Interface {
		// Hosts using the historical Native API may return ordinary Go
		// containers. Normalize them through the same bounded host bridge.
		switch value.(type) {
		case []byte, []any, []string, []int, []float64, []bool, map[string]any, map[string]string, map[string]int:
			guest, err := BridgeToGuest(value)
			if err != nil {
				return zero, err
			}
			return c.value(guest, t, depth)
		}
		dynamic := typeOfValue(c.vm, value)
		if s, ok := value.(*SliceVal); ok && s != nil && s.Fixed {
			dynamic = fmt.Sprintf("[%d]%s", len(s.Data), s.ElementType)
		}
		dt, err := c.typ(dynamic, depth)
		if err != nil {
			return zero, err
		}
		v, err := c.value(value, dt, depth)
		if err != nil {
			return zero, err
		}
		// Set/SetMapIndex accept concrete values assignable to an interface.
		// No temporary interface box is needed here.
		return v, nil
	}
	if identity != nil {
		if c.active[identity] {
			return zero, fmt.Errorf("json: cyclic value is unsupported")
		}
		if c.active == nil {
			c.active = make(map[any]bool)
		}
		c.active[identity] = true
		defer delete(c.active, identity)
	}
	// Exact scalar representations need neither allocation nor conversion.
	// Traversal counts, cancellation, string limits and cycle checks above
	// still run before this fast path.
	if rv := reflect.ValueOf(value); rv.IsValid() && rv.Type() == t {
		return rv, nil
	}
	out := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.Pointer:
		p, ok := value.(*PointerVal)
		if !ok {
			return zero, fmt.Errorf("json: expected guest pointer")
		}
		if p == nil || p.ref.kind == lvalueNil {
			return zero, nil
		}
		key := pointerHash(p)
		if previous := c.pointerValues[key]; c.preserveAliases && previous.IsValid() {
			return previous, nil
		}
		v, err := c.value(p.ref.get(), t.Elem(), depth+1)
		if err != nil {
			return zero, err
		}
		out.Set(reflect.New(t.Elem()))
		out.Elem().Set(v)
		if c.preserveAliases {
			if c.pointers == nil {
				c.pointers = make(map[uintptr]*PointerVal)
				c.pointerValues = make(map[string]reflect.Value)
			}
			c.pointers[out.Pointer()] = p
			c.pointerValues[key] = out
		}
	case reflect.Struct:
		s, ok := value.(*StructVal)
		if !ok || s == nil {
			return zero, fmt.Errorf("json: expected guest struct")
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			old, _ := s.field(f.Name)
			v, err := c.value(old, f.Type, depth+1)
			if err != nil {
				return zero, err
			}
			out.Field(i).Set(v)
		}
	case reflect.Slice, reflect.Array:
		s, ok := value.(*SliceVal)
		if !ok || s == nil {
			return zero, fmt.Errorf("json: expected guest slice")
		}
		if s.Data == nil && t.Kind() == reflect.Slice {
			return zero, nil
		}
		if previous := c.slices[s]; c.preserveAliases && previous.IsValid() {
			return previous, nil
		}
		input := s.Data
		if c.preserveAliases && t.Kind() == reflect.Slice {
			input = input[:cap(input)]
		}
		if len(input) > maxJSONValues-c.values {
			return zero, fmt.Errorf("json: value count limit exceeded")
		}
		if t.Kind() == reflect.Slice {
			out.Set(reflect.MakeSlice(t, len(s.Data), len(input)))
		}
		storage := out
		if t.Kind() == reflect.Slice {
			storage = out.Slice(0, len(input))
		}
		for i, v := range input {
			if i >= storage.Len() {
				return zero, fmt.Errorf("json: array length mismatch")
			}
			converted, err := c.value(v, t.Elem(), depth+1)
			if err != nil {
				return zero, err
			}
			storage.Index(i).Set(converted)
		}
		if c.preserveAliases {
			if c.slices == nil {
				c.slices = make(map[*SliceVal]reflect.Value)
			}
			c.slices[s] = out
		}
	case reflect.Map:
		m, ok := value.(*MapVal)
		if !ok || m == nil {
			return zero, fmt.Errorf("json: expected guest map")
		}
		if m.Data == nil {
			return zero, nil
		}
		if previous := c.maps[m]; c.preserveAliases && previous.IsValid() {
			return previous, nil
		}
		if len(m.Data) > maxJSONValues-c.values {
			return zero, fmt.Errorf("json: value count limit exceeded")
		}
		out.Set(reflect.MakeMapWithSize(t, len(m.Data)))
		for hash, value := range m.Data {
			k, err := c.value(m.originalKey(hash), t.Key(), depth+1)
			if err != nil {
				return zero, err
			}
			v, err := c.value(value, t.Elem(), depth+1)
			if err != nil {
				return zero, err
			}
			out.SetMapIndex(k, v)
		}
		if c.preserveAliases {
			if c.maps == nil {
				c.maps = make(map[*MapVal]reflect.Value)
			}
			c.maps[m] = out
		}
	default:
		rv := reflect.ValueOf(value)
		if !rv.IsValid() {
			return zero, nil
		}
		if !rv.Type().ConvertibleTo(t) {
			return zero, fmt.Errorf("json: %T is not assignable to %s", value, t)
		}
		if (t.Kind() == reflect.String && rv.Kind() != reflect.String) ||
			(t.Kind() == reflect.Bool && rv.Kind() != reflect.Bool) {
			return zero, fmt.Errorf("json: %T is not assignable to %s", value, t)
		}
		// Runtime integers represent guest integer types; conversion checks
		// avoid accepting an overflow hidden by that representation.
		if t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64 {
			if rv.Kind() < reflect.Int || rv.Kind() > reflect.Int64 || out.OverflowInt(rv.Int()) {
				return zero, fmt.Errorf("json: integer overflow or incompatible value")
			}
		}
		if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uintptr {
			if rv.Kind() >= reflect.Int && rv.Kind() <= reflect.Int64 {
				if rv.Int() < 0 || out.OverflowUint(uint64(rv.Int())) {
					return zero, fmt.Errorf("json: unsigned integer overflow")
				}
			} else if rv.Kind() < reflect.Uint || rv.Kind() > reflect.Uintptr || out.OverflowUint(rv.Uint()) {
				return zero, fmt.Errorf("json: unsigned integer overflow or incompatible value")
			}
		}
		out.Set(rv.Convert(t))
	}
	return out, nil
}

func (vm *Interpreter) marshalJSON(value any) ([]byte, error) {
	c := newJSONConversion(vm)
	v, err := c.value(value, reflect.TypeFor[any](), 0)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(v.Interface())
	if err == nil && len(data) > maxJSONBytes {
		return nil, fmt.Errorf("json: payload exceeds %d bytes", maxJSONBytes)
	}
	return data, err
}

func (vm *Interpreter) unmarshalJSON(data []byte, target any) error {
	p, ok := target.(*PointerVal)
	if !ok || p == nil || p.ref.kind == lvalueNil {
		return fmt.Errorf("json.Unmarshal: target must be a non-nil guest pointer")
	}
	if err := validateJSONEnvelope(data); err != nil {
		return err
	}
	c := newJSONConversion(vm)
	c.preserveAliases = true
	typ, err := c.typ(p.ElementType, 0)
	if err != nil {
		return err
	}
	value, err := c.value(p.ref.get(), typ, 0)
	if err != nil {
		return err
	}
	// Decode into a detached host value. Syntax errors are detected before
	// updates; encoding/json's ordinary type errors preserve partial changes.
	ptr := reflect.New(typ)
	ptr.Elem().Set(value)
	decodeErr := json.Unmarshal(data, ptr.Interface())
	converted, err := c.guest(ptr.Elem(), p.ElementType, p.ref.get(), 0)
	if err != nil {
		return err
	}
	if err = p.ref.set(converted); err != nil {
		return err
	}
	return decodeErr
}

func (c *jsonConversion) guest(value reflect.Value, typ string, old any, depth int) (any, error) {
	c.values++
	if c.values > maxJSONValues {
		return nil, fmt.Errorf("json: value count exceeds %d", maxJSONValues)
	}
	if c.values&255 == 0 {
		if err := c.vm.jsonStopError(); err != nil {
			return nil, err
		}
	}
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("json: nesting exceeds %d levels", maxJSONDepth)
	}
	if td := c.vm.types[typ]; td != nil && td.Kind == "named" {
		v, err := c.guest(value, td.Underlying, unwrapNamedValue(old), depth)
		if err != nil {
			return nil, err
		}
		return c.vm.coerceToType(v, typ), nil
	}
	typ = c.resolve(typ)
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil, nil
		}
		if inner := value.Elem(); inner.Kind() == reflect.Pointer && !inner.IsNil() {
			if pointer := c.pointers[inner.Pointer()]; pointer != nil {
				return c.guest(inner, "*"+pointer.ElementType, pointer, depth+1)
			}
		}
		return c.guest(value.Elem(), value.Elem().Type().String(), nil, depth+1)
	case reflect.Pointer:
		elem := strings.TrimPrefix(typ, "*")
		if value.IsNil() {
			return &PointerVal{ElementType: elem, ref: lvalueRef{kind: lvalueNil}}, nil
		}
		// Only pointers reused by encoding/json retain guest identity. A
		// replacement map entry receives a fresh pointer, matching Go.
		p := c.pointers[value.Pointer()]
		if p == nil || p.ref.kind == lvalueNil {
			if err := c.vm.chargeAllocation(1); err != nil {
				return nil, err
			}
			p = newPointerValue(c.vm.zeroValueForType(elem), elem)
		}
		v, err := c.guest(value.Elem(), elem, p.ref.get(), depth+1)
		if err != nil {
			return nil, err
		}
		return p, p.ref.set(v)
	case reflect.Struct:
		td := c.vm.types[typ]
		if td == nil {
			return nil, fmt.Errorf("json: unknown guest struct %s", typ)
		}
		if err := c.vm.chargeAllocation(uint64(len(td.Fields))); err != nil {
			return nil, err
		}
		s, ok := old.(*StructVal)
		if !ok || s == nil {
			s = c.vm.newPackedStruct(td)
			for _, f := range td.Fields {
				s.setField(f.Name, c.vm.zeroValueForType(f.Type))
			}
		}
		for i := 0; i < value.NumField(); i++ {
			name := value.Type().Field(i).Name
			var fieldType string
			for _, f := range td.Fields {
				if f.Name == name {
					fieldType = f.Type
					break
				}
			}
			prior, _ := s.field(name)
			v, err := c.guest(value.Field(i), fieldType, prior, depth+1)
			if err != nil {
				return nil, err
			}
			s.setField(name, v)
		}
		return s, nil
	case reflect.Slice, reflect.Array:
		elem := strings.TrimPrefix(typ, "[]")
		fixed := value.Kind() == reflect.Array
		if fixed {
			_, elem, _ = parseArrayType(typ)
		}
		s := &SliceVal{ElementType: elem, Fixed: fixed}
		if !fixed && value.IsNil() {
			return s, nil
		}
		if value.Len() > maxJSONValues-c.values {
			return nil, fmt.Errorf("json: value count exceeds %d", maxJSONValues)
		}
		if err := c.vm.chargeAllocation(uint64(value.Len())); err != nil {
			return nil, err
		}
		var previous []any
		reuseStorage := false
		reused := fixed
		if p, ok := old.(*SliceVal); ok && p != nil {
			previous = p.Data[:cap(p.Data)]
			retained := c.slices[p]
			if !fixed && value.Len() > 0 && retained.IsValid() && retained.Kind() == reflect.Slice {
				reused = value.Pointer() == retained.Pointer()
			}
			if reused && cap(previous) >= value.Len() {
				s.Data = previous[:value.Len()]
				reuseStorage = true
			} else if retained.IsValid() && retained.Kind() == reflect.Slice {
				// Replay precisely the writes performed before the host decoder
				// replaced its backing array. Array decoding writes before grow;
				// base64 decoding instead allocates without touching old bytes.
				storage := retained.Slice(0, retained.Cap())
				for i := 0; i < storage.Len(); i++ {
					v, err := c.guest(storage.Index(i), elem, previous[i], depth+1)
					if err != nil {
						return nil, err
					}
					previous[i] = v
				}
			}
		}
		if !reuseStorage {
			s.Data = make([]any, value.Len())
		}
		for i := 0; i < value.Len(); i++ {
			var prior any
			if reused && i < len(previous) {
				prior = previous[i]
			}
			v, err := c.guest(value.Index(i), elem, prior, depth+1)
			if err != nil {
				return nil, err
			}
			s.Data[i] = v
		}
		return s, nil
	case reflect.Map:
		kt, et := parseMapType(typ)
		m, ok := old.(*MapVal)
		if !ok || m == nil {
			m = &MapVal{KeyType: kt, ElementType: et}
		}
		if value.IsNil() {
			return &MapVal{KeyType: kt, ElementType: et}, nil
		}
		if value.Len() > (maxJSONValues-c.values)/2 {
			return nil, fmt.Errorf("json: value count exceeds %d", maxJSONValues)
		}
		if err := c.vm.chargeAllocation(uint64(value.Len())); err != nil {
			return nil, err
		}
		if m.Data == nil {
			m.Data = make(map[string]any, value.Len())
		}
		iter := value.MapRange()
		for iter.Next() {
			k, err := c.guest(iter.Key(), kt, nil, depth+1)
			if err != nil {
				return nil, err
			}
			prior, _ := m.getByKey(k)
			v, err := c.guest(iter.Value(), et, prior, depth+1)
			if err != nil {
				return nil, err
			}
			m.setByKey(k, v)
		}
		return m, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(value.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n := value.Uint()
		if uint64(int(n)) != n || int(n) < 0 {
			return nil, fmt.Errorf("json: unsigned value exceeds guest integer range")
		}
		return int(n), nil
	case reflect.Float32, reflect.Float64:
		return value.Float(), nil
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.String:
		return value.String(), nil
	default:
		return nil, fmt.Errorf("json: unsupported decoded type %s", value.Type())
	}
}
