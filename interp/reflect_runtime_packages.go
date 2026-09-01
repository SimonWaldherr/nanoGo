package interp

import (
	hostreflect "reflect"
	goruntime "runtime"
	runtimedebug "runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// reflectTypeInfo is nanoGo's compact, immutable description of a guest type.
// It deliberately describes guest values directly instead of repeatedly
// converting containers and asking the host reflect package about nanoGo's
// implementation structs.
type reflectTypeInfo struct {
	spelling string
	name     string
	kind     hostreflect.Kind
	elem     *reflectTypeInfo
	key      *reflectTypeInfo
	length   int
	fields   []FieldDef
}

type reflectValueInfo struct {
	value any
	typ   *reflectTypeInfo
	valid bool
}

// Keeping the native-state header and Value metadata in one allocation saves
// an interface-box allocation for every reflect.ValueOf call. The header may
// safely point into its own containing payload for the wrapper's lifetime.
type reflectValuePayload struct {
	info  reflectValueInfo
	state structNativeState
}

type reflectTypeCache struct {
	values map[string]*StructVal
}

var (
	reflectBoolType      = reflectTypeInfo{spelling: "bool", name: "bool", kind: hostreflect.Bool}
	reflectIntType       = reflectTypeInfo{spelling: "int", name: "int", kind: hostreflect.Int}
	reflectInt8Type      = reflectTypeInfo{spelling: "int8", name: "int8", kind: hostreflect.Int8}
	reflectInt16Type     = reflectTypeInfo{spelling: "int16", name: "int16", kind: hostreflect.Int16}
	reflectInt32Type     = reflectTypeInfo{spelling: "int32", name: "int32", kind: hostreflect.Int32}
	reflectRuneType      = reflectTypeInfo{spelling: "rune", name: "rune", kind: hostreflect.Int32}
	reflectInt64Type     = reflectTypeInfo{spelling: "int64", name: "int64", kind: hostreflect.Int64}
	reflectUintType      = reflectTypeInfo{spelling: "uint", name: "uint", kind: hostreflect.Uint}
	reflectUint8Type     = reflectTypeInfo{spelling: "uint8", name: "uint8", kind: hostreflect.Uint8}
	reflectByteType      = reflectTypeInfo{spelling: "byte", name: "byte", kind: hostreflect.Uint8}
	reflectUint16Type    = reflectTypeInfo{spelling: "uint16", name: "uint16", kind: hostreflect.Uint16}
	reflectUint32Type    = reflectTypeInfo{spelling: "uint32", name: "uint32", kind: hostreflect.Uint32}
	reflectUint64Type    = reflectTypeInfo{spelling: "uint64", name: "uint64", kind: hostreflect.Uint64}
	reflectUintptrType   = reflectTypeInfo{spelling: "uintptr", name: "uintptr", kind: hostreflect.Uintptr}
	reflectFloat32Type   = reflectTypeInfo{spelling: "float32", name: "float32", kind: hostreflect.Float32}
	reflectFloat64Type   = reflectTypeInfo{spelling: "float64", name: "float64", kind: hostreflect.Float64}
	reflectStringType    = reflectTypeInfo{spelling: "string", name: "string", kind: hostreflect.String}
	reflectFuncType      = reflectTypeInfo{spelling: "func", kind: hostreflect.Func}
	reflectAnyType       = reflectTypeInfo{spelling: "any", kind: hostreflect.Interface}
	reflectInterfaceType = reflectTypeInfo{spelling: "interface{}", kind: hostreflect.Interface}
	reflectErrorType     = reflectTypeInfo{spelling: "error", kind: hostreflect.Interface}
)

func primitiveReflectType(name string) *reflectTypeInfo {
	switch name {
	case "bool":
		return &reflectBoolType
	case "int":
		return &reflectIntType
	case "int8":
		return &reflectInt8Type
	case "int16":
		return &reflectInt16Type
	case "int32":
		return &reflectInt32Type
	case "rune":
		return &reflectRuneType
	case "int64":
		return &reflectInt64Type
	case "uint":
		return &reflectUintType
	case "uint8":
		return &reflectUint8Type
	case "byte":
		return &reflectByteType
	case "uint16":
		return &reflectUint16Type
	case "uint32":
		return &reflectUint32Type
	case "uint64":
		return &reflectUint64Type
	case "uintptr":
		return &reflectUintptrType
	case "float32":
		return &reflectFloat32Type
	case "float64":
		return &reflectFloat64Type
	case "string":
		return &reflectStringType
	case "func":
		return &reflectFuncType
	case "any":
		return &reflectAnyType
	case "interface{}":
		return &reflectInterfaceType
	case "error":
		return &reflectErrorType
	default:
		return nil
	}
}

func reflectTypeForName(vm *Interpreter, typ string) *reflectTypeInfo {
	typ = strings.TrimSpace(typ)
	if info := primitiveReflectType(typ); info != nil {
		return info
	}
	if td := vm.types[typ]; td != nil {
		switch td.Kind {
		case "alias":
			info := reflectTypeForName(vm, td.Underlying)
			copy := *info
			copy.spelling, copy.name = typ, typ
			return &copy
		case "interface":
			return &reflectTypeInfo{spelling: typ, name: typ, kind: hostreflect.Interface}
		case "struct":
			return &reflectTypeInfo{spelling: typ, name: typ, kind: hostreflect.Struct, fields: td.Fields}
		}
	}
	if length, elem, ok := parseArrayType(typ); ok {
		return &reflectTypeInfo{spelling: typ, kind: hostreflect.Array, elem: reflectTypeForName(vm, elem), length: length}
	}
	if strings.HasPrefix(typ, "[]") {
		return &reflectTypeInfo{spelling: typ, kind: hostreflect.Slice, elem: reflectTypeForName(vm, typ[2:])}
	}
	if strings.HasPrefix(typ, "map[") {
		key, elem := parseMapType(typ)
		return &reflectTypeInfo{spelling: typ, kind: hostreflect.Map, key: reflectTypeForName(vm, key), elem: reflectTypeForName(vm, elem)}
	}
	if strings.HasPrefix(typ, "chan ") {
		return &reflectTypeInfo{spelling: typ, kind: hostreflect.Chan, elem: reflectTypeForName(vm, strings.TrimSpace(typ[5:]))}
	}
	if strings.HasPrefix(typ, "*") {
		return &reflectTypeInfo{spelling: typ, kind: hostreflect.Ptr, elem: reflectTypeForName(vm, typ[1:])}
	}
	return &reflectTypeInfo{spelling: typ, name: typ, kind: hostreflect.Struct}
}

func reflectTypeForValue(vm *Interpreter, value any) *reflectTypeInfo {
	switch v := value.(type) {
	case *SliceVal:
		if v == nil {
			return nil
		}
		kind, spelling := hostreflect.Slice, "[]"+v.ElementType
		if v.Fixed {
			kind, spelling = hostreflect.Array, "["+strconv.Itoa(len(v.Data))+"]"+v.ElementType
		}
		return &reflectTypeInfo{spelling: spelling, kind: kind, elem: reflectTypeForName(vm, v.ElementType), length: len(v.Data)}
	case *MapVal:
		if v == nil {
			return nil
		}
		return &reflectTypeInfo{spelling: "map[" + v.KeyType + "]" + v.ElementType, kind: hostreflect.Map, key: reflectTypeForName(vm, v.KeyType), elem: reflectTypeForName(vm, v.ElementType)}
	case *ChannelVal:
		if v == nil {
			return nil
		}
		return &reflectTypeInfo{spelling: "chan " + v.ElementType, kind: hostreflect.Chan, elem: reflectTypeForName(vm, v.ElementType)}
	case *StructVal:
		if v == nil {
			return nil
		}
		return reflectTypeForName(vm, v.TypeName)
	case *Function:
		return reflectTypeForName(vm, "func")
	case int:
		return reflectTypeForName(vm, "int")
	case int64:
		return reflectTypeForName(vm, "int64")
	case float64:
		return reflectTypeForName(vm, "float64")
	case bool:
		return reflectTypeForName(vm, "bool")
	case string:
		return reflectTypeForName(vm, "string")
	case nil:
		return nil
	default:
		t := hostreflect.TypeOf(value)
		if t == nil {
			return nil
		}
		return &reflectTypeInfo{spelling: t.String(), name: t.Name(), kind: t.Kind()}
	}
}

func reflectTypeNameForValue(vm *Interpreter, value any) string {
	if slice, ok := value.(*SliceVal); ok && slice != nil && slice.Fixed {
		return "[" + strconv.Itoa(len(slice.Data)) + "]" + slice.ElementType
	}
	return typeOfValue(vm, value)
}

func reflectNativeState(value any, typeName string) (any, error) {
	sv, ok := value.(*StructVal)
	if !ok || sv == nil || sv.TypeName != typeName {
		return nil, NewRuntimeError("reflect: invalid " + strings.TrimPrefix(typeName, "reflect.") + " value")
	}
	state := sv.nativeState.Load()
	if state == nil {
		return nil, NewRuntimeError("reflect: uninitialized " + strings.TrimPrefix(typeName, "reflect.") + " value")
	}
	return state.value, nil
}

func reflectTypeSize(vm *Interpreter, info *reflectTypeInfo) int {
	word := strconv.IntSize / 8
	switch info.kind {
	case hostreflect.Bool, hostreflect.Int8, hostreflect.Uint8:
		return 1
	case hostreflect.Int16, hostreflect.Uint16:
		return 2
	case hostreflect.Int32, hostreflect.Uint32, hostreflect.Float32:
		return 4
	case hostreflect.Int64, hostreflect.Uint64, hostreflect.Float64:
		return 8
	case hostreflect.Int, hostreflect.Uint, hostreflect.Uintptr, hostreflect.Chan, hostreflect.Func, hostreflect.Map, hostreflect.Ptr, hostreflect.UnsafePointer:
		return word
	case hostreflect.Interface, hostreflect.String:
		return 2 * word
	case hostreflect.Slice:
		return 3 * word
	case hostreflect.Array:
		return info.length * reflectTypeSize(vm, info.elem)
	case hostreflect.Struct:
		size := 0
		for _, field := range info.fields {
			size += reflectTypeSize(vm, reflectTypeForName(vm, field.Type))
		}
		return size
	default:
		return 0
	}
}

type reflectDeepVisit struct {
	left  any
	right any
}

func reflectDeepEqual(left, right any) bool {
	return reflectDeepEqualSeen(left, right, make(map[reflectDeepVisit]struct{}))
}

func reflectDeepEqualSeen(left, right any, seen map[reflectDeepVisit]struct{}) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	visit := func(left, right any) bool {
		key := reflectDeepVisit{left: left, right: right}
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		return false
	}
	switch l := left.(type) {
	case *StructVal:
		r, ok := right.(*StructVal)
		if !ok || l == nil || r == nil {
			return ok && l == nil && r == nil
		}
		if l == r {
			return true
		}
		if l.TypeName == "reflect.Type" && r.TypeName == l.TypeName {
			ls, rs := l.nativeState.Load(), r.nativeState.Load()
			if ls == nil || rs == nil {
				return ls == nil && rs == nil
			}
			li, lok := ls.value.(*reflectTypeInfo)
			ri, rok := rs.value.(*reflectTypeInfo)
			return lok && rok && li.spelling == ri.spelling
		}
		if l.TypeName == "reflect.Value" && r.TypeName == l.TypeName {
			li, lerr := reflectValueArg(l)
			ri, rerr := reflectValueArg(r)
			if lerr != nil || rerr != nil || li.valid != ri.valid {
				return false
			}
			if !li.valid {
				return true
			}
			if li.typ == nil || ri.typ == nil || li.typ.spelling != ri.typ.spelling {
				return false
			}
			return reflectDeepEqualSeen(li.value, ri.value, seen)
		}
		if l.TypeName != r.TypeName || l.fieldCount() != r.fieldCount() || visit(l, r) {
			return l.TypeName == r.TypeName && l.fieldCount() == r.fieldCount()
		}
		equal := true
		l.forEachField(func(name string, value any) {
			other, ok := r.field(name)
			if !ok || !reflectDeepEqualSeen(value, other, seen) {
				equal = false
			}
		})
		return equal
	case *SliceVal:
		r, ok := right.(*SliceVal)
		if !ok || l == nil || r == nil {
			return ok && l == nil && r == nil
		}
		if l == r {
			return true
		}
		if l.ElementType != r.ElementType || l.Fixed != r.Fixed || len(l.Data) != len(r.Data) {
			return false
		}
		if visit(l, r) {
			return true
		}
		for i := range l.Data {
			if !reflectDeepEqualSeen(l.Data[i], r.Data[i], seen) {
				return false
			}
		}
		return true
	case *MapVal:
		r, ok := right.(*MapVal)
		if !ok || l == nil || r == nil {
			return ok && l == nil && r == nil
		}
		if l == r {
			return true
		}
		if l.KeyType != r.KeyType || l.ElementType != r.ElementType || len(l.Data) != len(r.Data) {
			return false
		}
		if visit(l, r) {
			return true
		}
		for key, value := range l.Data {
			other, ok := r.Data[key]
			if !ok || !reflectDeepEqualSeen(value, other, seen) {
				return false
			}
		}
		return true
	case *ChannelVal:
		r, ok := right.(*ChannelVal)
		return ok && l == r
	case *Function:
		// Non-nil functions are never deeply equal in Go.
		_, ok := right.(*Function)
		return !ok && hostreflect.DeepEqual(left, right)
	default:
		return hostreflect.DeepEqual(left, right)
	}
}

func reflectValueArg(value any) (reflectValueInfo, error) {
	state, err := reflectNativeState(value, "reflect.Value")
	if err != nil {
		return reflectValueInfo{}, err
	}
	info, ok := state.(*reflectValueInfo)
	if !ok || info == nil {
		return reflectValueInfo{}, NewRuntimeError("reflect: corrupt Value")
	}
	return *info, nil
}

func registerReflectPackage(vm *Interpreter) {
	typeDef := &TypeDef{Name: "reflect.Type", Kind: "struct", Methods: map[string]*Function{}}
	valueDef := &TypeDef{Name: "reflect.Value", Kind: "struct", Methods: map[string]*Function{}}
	tagDef := &TypeDef{Name: "reflect.StructTag", Kind: "struct", Methods: map[string]*Function{}}
	tagDef.Methods["Get"] = &Function{Name: "Get", RecvType: tagDef.Name, Params: []string{"key"}, Native: func(args []any) (any, error) {
		tag, ok := args[0].(*StructVal)
		if !ok {
			return "", NewRuntimeError("reflect: StructTag.Get on non-tag")
		}
		raw, _ := tag.field("__value")
		value, _ := structTagValue(ToString(raw), ToString(args[1]))
		return value, nil
	}}
	fieldDef := &TypeDef{Name: "reflect.StructField", Kind: "struct", Fields: []FieldDef{{Name: "Name", Type: "string"}, {Name: "Type", Type: "reflect.Type"}, {Name: "Index", Type: "int"}, {Name: "Tag", Type: "reflect.StructTag"}}, Methods: map[string]*Function{}}
	vm.types[typeDef.Name], vm.types[valueDef.Name], vm.types[fieldDef.Name], vm.types[tagDef.Name] = typeDef, valueDef, fieldDef, tagDef

	// Type values are immutable. Cache them by spelling so TypeOf in a guest
	// inspection loop allocates only on the first occurrence of each type.
	var typeMu sync.Mutex
	var typeCache atomic.Pointer[reflectTypeCache]
	typeCache.Store(&reflectTypeCache{values: make(map[string]*StructVal)})
	wrapType := func(info *reflectTypeInfo) any {
		if info == nil {
			return nil
		}
		if value := typeCache.Load().values[info.spelling]; value != nil {
			return value
		}
		typeMu.Lock()
		current := typeCache.Load()
		value := current.values[info.spelling]
		if value == nil {
			value = &StructVal{TypeName: "reflect.Type"}
			value.nativeState.Store(&structNativeState{value: info})
			next := make(map[string]*StructVal, len(current.values)+1)
			for name, cached := range current.values {
				next[name] = cached
			}
			next[info.spelling] = value
			typeCache.Store(&reflectTypeCache{values: next})
		}
		typeMu.Unlock()
		return value
	}
	wrapTypeName := func(name string) any {
		if value := typeCache.Load().values[name]; value != nil {
			return value
		}
		return wrapType(reflectTypeForName(vm, name))
	}
	wrapValue := func(value any, typ *reflectTypeInfo, valid bool) *StructVal {
		out := &StructVal{TypeName: "reflect.Value"}
		payload := &reflectValuePayload{info: reflectValueInfo{value: value, typ: typ, valid: valid}}
		payload.state.value = &payload.info
		out.nativeState.Store(&payload.state)
		return out
	}
	typeArg := func(value any) (*reflectTypeInfo, error) {
		state, err := reflectNativeState(value, "reflect.Type")
		if err != nil {
			return nil, err
		}
		info, ok := state.(*reflectTypeInfo)
		if !ok {
			return nil, NewRuntimeError("reflect: corrupt Type")
		}
		return info, nil
	}

	typeDef.Methods["Kind"] = &Function{Name: "Kind", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		return int(info.kind), nil
	}}
	typeDef.Methods["String"] = &Function{Name: "String", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		return info.spelling, nil
	}}
	typeDef.Methods["Name"] = &Function{Name: "Name", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		return info.name, nil
	}}
	typeDef.Methods["Elem"] = &Function{Name: "Elem", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		if info.elem == nil {
			return nil, NewRuntimeError("reflect: Elem of " + info.spelling)
		}
		return wrapType(info.elem), nil
	}}
	typeDef.Methods["Key"] = &Function{Name: "Key", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		if info.key == nil {
			return nil, NewRuntimeError("reflect: Key of " + info.spelling)
		}
		return wrapType(info.key), nil
	}}
	typeDef.Methods["Len"] = &Function{Name: "Len", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		if info.kind != hostreflect.Array {
			return nil, NewRuntimeError("reflect: Len of non-array type")
		}
		return info.length, nil
	}}
	typeDef.Methods["NumField"] = &Function{Name: "NumField", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		if info.kind != hostreflect.Struct {
			return nil, NewRuntimeError("reflect: NumField of non-struct type")
		}
		return len(info.fields), nil
	}}
	typeDef.Methods["Field"] = &Function{Name: "Field", RecvType: typeDef.Name, Params: []string{"i"}, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		i := ToInt(args[1])
		if i < 0 || i >= len(info.fields) {
			return nil, NewRuntimeError("reflect: field index out of range")
		}
		field := info.fields[i]
		tag := &StructVal{TypeName: tagDef.Name, Fields: map[string]any{"__value": field.Tag}}
		return &StructVal{TypeName: fieldDef.Name, Fields: map[string]any{"Name": field.Name, "Type": wrapType(reflectTypeForName(vm, field.Type)), "Index": i, "Tag": tag}}, nil
	}}
	typeDef.Methods["Size"] = &Function{Name: "Size", RecvType: typeDef.Name, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		return reflectTypeSize(vm, info), nil
	}}

	valueDef.Methods["IsValid"] = &Function{Name: "IsValid", RecvType: valueDef.Name, Native: func(args []any) (any, error) { v, err := reflectValueArg(args[0]); return v.valid, err }}
	valueDef.Methods["Kind"] = &Function{Name: "Kind", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		if !v.valid || v.typ == nil {
			return int(hostreflect.Invalid), nil
		}
		return int(v.typ.kind), nil
	}}
	valueDef.Methods["Type"] = &Function{Name: "Type", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		if !v.valid {
			return nil, NewRuntimeError("reflect: Type of zero Value")
		}
		return wrapType(v.typ), nil
	}}
	valueDef.Methods["Interface"] = &Function{Name: "Interface", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		if !v.valid {
			return nil, NewRuntimeError("reflect: Interface of zero Value")
		}
		return v.value, nil
	}}
	valueDef.Methods["CanInterface"] = &Function{Name: "CanInterface", RecvType: valueDef.Name, Native: func(args []any) (any, error) { v, err := reflectValueArg(args[0]); return v.valid, err }}
	valueDef.Methods["IsNil"] = &Function{Name: "IsNil", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		if !v.valid || v.typ == nil {
			return false, NewRuntimeError("reflect: IsNil of invalid Value")
		}
		switch v.typ.kind {
		case hostreflect.Chan, hostreflect.Func, hostreflect.Interface, hostreflect.Map, hostreflect.Ptr, hostreflect.Slice:
			return v.value == nil, nil
		}
		return false, NewRuntimeError("reflect: IsNil of " + v.typ.spelling)
	}}
	valueDef.Methods["IsZero"] = &Function{Name: "IsZero", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		if !v.valid {
			return nil, NewRuntimeError("reflect: IsZero of invalid Value")
		}
		return reflectDeepEqual(v.value, vm.zeroValueForType(v.typ.spelling)), nil
	}}
	valueDef.Methods["Len"] = &Function{Name: "Len", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		switch x := v.value.(type) {
		case string:
			return len(x), nil
		case *SliceVal:
			return len(x.Data), nil
		case *MapVal:
			return len(x.Data), nil
		case *ChannelVal:
			return len(x.C), nil
		}
		return nil, NewRuntimeError("reflect: Len of unsupported value")
	}}
	valueDef.Methods["Cap"] = &Function{Name: "Cap", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		switch x := v.value.(type) {
		case *SliceVal:
			return cap(x.Data), nil
		case *ChannelVal:
			return cap(x.C), nil
		}
		return nil, NewRuntimeError("reflect: Cap of unsupported value")
	}}
	valueDef.Methods["Index"] = &Function{Name: "Index", RecvType: valueDef.Name, Params: []string{"i"}, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		i := ToInt(args[1])
		switch x := v.value.(type) {
		case string:
			if i < 0 || i >= len(x) {
				return nil, NewRuntimeError("reflect: index out of range")
			}
			return wrapValue(int(x[i]), reflectTypeForName(vm, "byte"), true), nil
		case *SliceVal:
			if i < 0 || i >= len(x.Data) {
				return nil, NewRuntimeError("reflect: index out of range")
			}
			return wrapValue(x.Data[i], v.typ.elem, true), nil
		}
		return nil, NewRuntimeError("reflect: Index of unsupported value")
	}}
	valueDef.Methods["MapIndex"] = &Function{Name: "MapIndex", RecvType: valueDef.Name, Params: []string{"key"}, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		m, ok := v.value.(*MapVal)
		if !ok {
			return nil, NewRuntimeError("reflect: MapIndex of non-map")
		}
		key := args[1]
		if kv, e := reflectValueArg(key); e == nil {
			key = kv.value
		}
		value, found := m.getByKey(key)
		return wrapValue(value, v.typ.elem, found), nil
	}}
	valueDef.Methods["Field"] = &Function{Name: "Field", RecvType: valueDef.Name, Params: []string{"i"}, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		s, ok := v.value.(*StructVal)
		if !ok || v.typ == nil {
			return nil, NewRuntimeError("reflect: Field of non-struct")
		}
		i := ToInt(args[1])
		if i < 0 || i >= len(v.typ.fields) {
			return nil, NewRuntimeError("reflect: field index out of range")
		}
		f := v.typ.fields[i]
		value, _ := s.field(f.Name)
		return wrapValue(value, reflectTypeForName(vm, f.Type), true), nil
	}}
	valueDef.Methods["FieldByName"] = &Function{Name: "FieldByName", RecvType: valueDef.Name, Params: []string{"name"}, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		s, ok := v.value.(*StructVal)
		if !ok {
			return nil, NewRuntimeError("reflect: FieldByName of non-struct")
		}
		name := ToString(args[1])
		value, found := s.field(name)
		var typ *reflectTypeInfo
		if found && v.typ != nil {
			for _, f := range v.typ.fields {
				if f.Name == name {
					typ = reflectTypeForName(vm, f.Type)
					break
				}
			}
		}
		return wrapValue(value, typ, found), nil
	}}
	valueDef.Methods["Int"] = &Function{Name: "Int", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		return ToInt(v.value), nil
	}}
	valueDef.Methods["Float"] = &Function{Name: "Float", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		return ToFloat(v.value), nil
	}}
	valueDef.Methods["Bool"] = &Function{Name: "Bool", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		return ToBool(v.value), nil
	}}
	valueDef.Methods["String"] = &Function{Name: "String", RecvType: valueDef.Name, Native: func(args []any) (any, error) {
		v, err := reflectValueArg(args[0])
		if err != nil {
			return nil, err
		}
		return ToString(v.value), nil
	}}

	reflectPkg := &Package{Name: "reflect", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"Type": typeDef, "Value": valueDef, "StructField": fieldDef, "StructTag": tagDef}, Vars: map[string]any{
		"Invalid": int(hostreflect.Invalid), "Bool": int(hostreflect.Bool), "Int": int(hostreflect.Int),
		"Int8": int(hostreflect.Int8), "Int16": int(hostreflect.Int16), "Int32": int(hostreflect.Int32), "Int64": int(hostreflect.Int64),
		"Uint": int(hostreflect.Uint), "Uint8": int(hostreflect.Uint8), "Uint16": int(hostreflect.Uint16), "Uint32": int(hostreflect.Uint32),
		"Uint64": int(hostreflect.Uint64), "Uintptr": int(hostreflect.Uintptr), "Float32": int(hostreflect.Float32), "Float64": int(hostreflect.Float64),
		"Complex64": int(hostreflect.Complex64), "Complex128": int(hostreflect.Complex128), "Array": int(hostreflect.Array), "Chan": int(hostreflect.Chan),
		"Func": int(hostreflect.Func), "Interface": int(hostreflect.Interface), "Map": int(hostreflect.Map), "Ptr": int(hostreflect.Ptr),
		"Slice": int(hostreflect.Slice), "String": int(hostreflect.String), "Struct": int(hostreflect.Struct), "UnsafePointer": int(hostreflect.UnsafePointer),
	}}
	reflectPkg.Funcs["TypeOf"] = &Function{Name: "TypeOf", Params: []string{"value"}, Native: func(args []any) (any, error) {
		if len(args) == 0 || args[0] == nil {
			return nil, nil
		}
		return wrapTypeName(reflectTypeNameForValue(vm, args[0])), nil
	}}
	reflectPkg.Funcs["ValueOf"] = &Function{Name: "ValueOf", Params: []string{"value"}, Native: func(args []any) (any, error) {
		if len(args) == 0 || args[0] == nil {
			return wrapValue(nil, nil, false), nil
		}
		return wrapValue(args[0], reflectTypeForValue(vm, args[0]), true), nil
	}}
	reflectPkg.Funcs["DeepEqual"] = &Function{Name: "DeepEqual", Params: []string{"a", "b"}, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return false, NewRuntimeError("reflect.DeepEqual: expected two arguments")
		}
		return reflectDeepEqual(args[0], args[1]), nil
	}}
	reflectPkg.Funcs["Zero"] = &Function{Name: "Zero", Params: []string{"type"}, Native: func(args []any) (any, error) {
		info, err := typeArg(args[0])
		if err != nil {
			return nil, err
		}
		return wrapValue(vm.zeroValueForType(info.spelling), info, true), nil
	}}
	vm.RegisterPackage("reflect", reflectPkg)
}

func runtimeMemStatsValue() *StructVal {
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	return &StructVal{TypeName: "runtime.MemStats", Fields: map[string]any{
		"Alloc": int(stats.Alloc), "TotalAlloc": int(stats.TotalAlloc), "Sys": int(stats.Sys),
		"Mallocs": int(stats.Mallocs), "Frees": int(stats.Frees), "HeapAlloc": int(stats.HeapAlloc),
		"HeapSys": int(stats.HeapSys), "HeapObjects": int(stats.HeapObjects), "NumGC": int(stats.NumGC),
		"PauseTotalNs": int(stats.PauseTotalNs),
	}}
}

func registerRuntimePackage(vm *Interpreter) {
	memFields := []FieldDef{{Name: "Alloc", Type: "uint64"}, {Name: "TotalAlloc", Type: "uint64"}, {Name: "Sys", Type: "uint64"}, {Name: "Mallocs", Type: "uint64"}, {Name: "Frees", Type: "uint64"}, {Name: "HeapAlloc", Type: "uint64"}, {Name: "HeapSys", Type: "uint64"}, {Name: "HeapObjects", Type: "uint64"}, {Name: "NumGC", Type: "uint32"}, {Name: "PauseTotalNs", Type: "uint64"}}
	memType := &TypeDef{Name: "runtime.MemStats", Kind: "struct", Fields: memFields, Methods: map[string]*Function{}}
	vm.types[memType.Name] = memType
	runtimePkg := &Package{Name: "runtime", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"MemStats": memType}, Vars: map[string]any{"GOOS": goruntime.GOOS, "GOARCH": goruntime.GOARCH}}
	runtimePkg.Funcs["Version"] = &Function{Name: "Version", Native: func([]any) (any, error) { return goruntime.Version(), nil }}
	runtimePkg.Funcs["NumCPU"] = &Function{Name: "NumCPU", Native: func([]any) (any, error) { return goruntime.NumCPU(), nil }}
	runtimePkg.Funcs["NumGoroutine"] = &Function{Name: "NumGoroutine", Native: func([]any) (any, error) {
		if vm.activeExecution != nil {
			return int(vm.activeExecution.goroutines.Load()), nil
		}
		return 0, nil
	}}
	runtimePkg.Funcs["GOMAXPROCS"] = &Function{Name: "GOMAXPROCS", Params: []string{"n"}, Native: func(args []any) (any, error) {
		n := 0
		if len(args) != 0 {
			n = ToInt(args[0])
		}
		if n != 0 {
			return nil, NewRuntimeError("runtime.GOMAXPROCS: changing the host process is not permitted")
		}
		return goruntime.GOMAXPROCS(0), nil
	}}
	runtimePkg.Funcs["Gosched"] = &Function{Name: "Gosched", Native: func([]any) (any, error) { goruntime.Gosched(); return nil, nil }}
	runtimePkg.Funcs["GC"] = &Function{Name: "GC", Native: func([]any) (any, error) { goruntime.GC(); return nil, nil }}
	runtimePkg.Funcs["ReadMemStats"] = &Function{Name: "ReadMemStats", Native: func([]any) (any, error) { return runtimeMemStatsValue(), nil }}
	// Stack is intercepted in evalExpr because it needs the guest call frame.
	runtimePkg.Funcs["Stack"] = &Function{Name: "Stack", Params: []string{"buf", "all"}, Native: func([]any) (any, error) { return nil, NewRuntimeError("runtime.Stack: no active guest frame") }}
	vm.RegisterPackage("runtime", runtimePkg)
}

func runtimeBuildInfoValue(info *runtimedebug.BuildInfo) *StructVal {
	if info == nil {
		return nil
	}
	return &StructVal{TypeName: "runtime/debug.BuildInfo", Fields: map[string]any{"GoVersion": info.GoVersion, "Path": info.Path, "Main": info.Main.Path}}
}

func registerRuntimeDebugPackage(vm *Interpreter) {
	buildType := &TypeDef{Name: "runtime/debug.BuildInfo", Kind: "struct", Fields: []FieldDef{{Name: "GoVersion", Type: "string"}, {Name: "Path", Type: "string"}, {Name: "Main", Type: "string"}}, Methods: map[string]*Function{}}
	vm.types[buildType.Name] = buildType
	debugPkg := &Package{Name: "runtime/debug", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"BuildInfo": buildType}}
	debugPkg.Funcs["Stack"] = &Function{Name: "Stack", Native: func([]any) (any, error) { return nil, NewRuntimeError("runtime/debug.Stack: no active guest frame") }}
	debugPkg.Funcs["PrintStack"] = &Function{Name: "PrintStack", Native: func([]any) (any, error) {
		return nil, NewRuntimeError("runtime/debug.PrintStack: no active guest frame")
	}}
	debugPkg.Funcs["ReadBuildInfo"] = &Function{Name: "ReadBuildInfo", Native: func([]any) (any, error) {
		info, ok := runtimedebug.ReadBuildInfo()
		if !ok {
			return nil, nil
		}
		return runtimeBuildInfoValue(info), nil
	}}
	debugPkg.Funcs["ParseBuildInfo"] = &Function{Name: "ParseBuildInfo", Params: []string{"data"}, Native: func(args []any) (any, error) {
		info, err := runtimedebug.ParseBuildInfo(ToString(args[0]))
		if err != nil {
			return nil, err
		}
		return runtimeBuildInfoValue(info), nil
	}}
	// These settings are deliberately interpreter-local. Letting a guest alter
	// host-global GC policy would make unrelated interpreters slower.
	var gcPercent atomic.Int64
	gcPercent.Store(100)
	debugPkg.Funcs["SetGCPercent"] = &Function{Name: "SetGCPercent", Params: []string{"percent"}, Native: func(args []any) (any, error) { return int(gcPercent.Swap(int64(ToInt(args[0])))), nil }}
	var memoryLimit atomic.Int64
	memoryLimit.Store(int64(^uint64(0) >> 1))
	debugPkg.Funcs["SetMemoryLimit"] = &Function{Name: "SetMemoryLimit", Params: []string{"limit"}, Native: func(args []any) (any, error) { return int(memoryLimit.Swap(int64(ToInt(args[0])))), nil }}
	debugPkg.Funcs["FreeOSMemory"] = &Function{Name: "FreeOSMemory", Native: func([]any) (any, error) { return nil, nil }}
	vm.RegisterPackage("runtime/debug", debugPkg)
}
