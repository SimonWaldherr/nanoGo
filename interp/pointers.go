package interp

import (
	"fmt"
	"go/ast"
	"sync"
)

// PointerVal holds an addressable guest location rather than a copied value.
// The location retains its owning scope or backing array for its lifetime.
type PointerVal struct {
	ElementType string
	ref         lvalueRef
}

type pointerCell struct {
	mu    sync.RWMutex
	value any
}

func newPointerValue(value any, typ string) *PointerVal {
	return &PointerVal{ElementType: typ, ref: lvalueRef{kind: lvalueCell, cell: &pointerCell{value: value}}}
}

func pointerLocation(value any) (*PointerVal, error) {
	if value == nil {
		return nil, &panicError{value: "runtime error: invalid memory address or nil pointer dereference"}
	}
	ptr, ok := value.(*PointerVal)
	if !ok {
		return nil, NewRuntimeError("cannot dereference non-pointer")
	}
	if ptr == nil || ptr.ref.kind == lvalueNil {
		return nil, &panicError{value: "runtime error: invalid memory address or nil pointer dereference"}
	}
	return ptr, nil
}

func dereference(value any) (any, error) {
	ptr, err := pointerLocation(value)
	if err != nil {
		return nil, err
	}
	return ptr.ref.get(), nil
}

func structReceiver(value any) (any, error) {
	if _, ok := value.(*PointerVal); ok {
		return dereference(value)
	}
	return value, nil
}

func (vm *Interpreter) addressOf(expr ast.Expr, env *Env) (any, error) {
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return vm.addressOf(paren.X, env)
	}
	if literal, ok := expr.(*ast.CompositeLit); ok {
		value, err := vm.evalExpr(literal, env)
		if err != nil {
			return nil, err
		}
		return newPointerValue(value, typeString(literal.Type)), nil
	}
	ref, err := vm.resolveLvalue(expr, env)
	if err != nil {
		return nil, err
	}
	switch ref.kind {
	case lvalueMapIndex:
		return nil, NewRuntimeError("cannot take address of map element")
	case lvalueVar:
		owner := ref.env
		for owner != nil && !vm.hasLocalBinding(ref.name, owner) {
			owner = owner.Parent
		}
		if owner == nil || ref.name == "_" {
			return nil, NewRuntimeError("cannot take address of undefined variable")
		}
		ref.env = owner
	case lvalueSliceIndex:
		// Freeze the slice header, not its elements; append may change the
		// caller's header, but must not redirect an existing element pointer.
		header := *ref.s
		ref.s = &header
	}
	return &PointerVal{ElementType: typeOfValue(vm, ref.get()), ref: ref}, nil
}

func samePointer(a, b *PointerVal) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := a.ref, b.ref
	if x.kind != y.kind {
		return false
	}
	switch x.kind {
	case lvalueNil:
		return a.ElementType == b.ElementType
	case lvalueVar:
		return x.env == y.env && x.name == y.name
	case lvalueSliceIndex:
		return &x.s.Data[x.i] == &y.s.Data[y.i]
	case lvalueField:
		return x.sv == y.sv && x.name == y.name
	case lvalueCell:
		return x.cell == y.cell
	}
	return false
}

func nilGuestReference(value any) bool {
	switch v := value.(type) {
	case *PointerVal:
		return v == nil || v.ref.kind == lvalueNil
	case *ChannelVal:
		return v == nil || v.C == nil
	default:
		return value == nil
	}
}

func pointerHash(pointer *PointerVal) string {
	if pointer == nil {
		return "pointer:nil"
	}
	r := pointer.ref
	prefix := "pointer:" + pointer.ElementType + ":"
	switch r.kind {
	case lvalueVar:
		return prefix + fmt.Sprintf("var:%p:%s", r.env, r.name)
	case lvalueSliceIndex:
		return prefix + fmt.Sprintf("element:%p", &r.s.Data[r.i])
	case lvalueField:
		return prefix + fmt.Sprintf("field:%p:%s", r.sv, r.name)
	case lvalueCell:
		return prefix + fmt.Sprintf("cell:%p", r.cell)
	default:
		return prefix + "nil"
	}
}
