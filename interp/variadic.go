package interp

import (
	"go/ast"
	"go/token"
)

// spreadSlice preserves the caller's backing array for a guest f(xs...) call.
// Capture the header now: defer/go must not observe a later slice reassignment.
type spreadSlice struct{ value SliceVal }

func appendSpreadArguments(fn *Function, args []any, slice *SliceVal) []any {
	if fn.Native == nil && fn.NativeContext == nil && fn.IsVariadic {
		return append(args, spreadSlice{value: *slice})
	}
	return append(args, slice.Data...)
}

func preparedArguments(fn *Function, recv *any, call *ast.CallExpr, args []any) (*Function, *any, []any, error) {
	if call.Ellipsis != token.NoPos && len(args) != 0 {
		last := len(args) - 1
		if slice, ok := args[last].(*SliceVal); ok {
			args = appendSpreadArguments(fn, args[:last], slice)
		} else if args[last] == nil {
			args = appendSpreadArguments(fn, args[:last], &SliceVal{ElementType: "any"})
		} else {
			return nil, nil, nil, NewRuntimeError("variadic expansion requires a slice")
		}
	}
	return fn, recv, args, nil
}
