package interp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// ContextField explicitly selects one Go context value to expose to nanoGo.
// Context values cannot be enumerated, which makes this an allow-list rather
// than an accidental pass-through of every request value.
//
// Name is the key under hostContext["values"] in guest code. Key is the exact
// key used with context.WithValue by the host application.
type ContextField struct {
	Name string
	Key  any
}

// ContextSnapshot returns a safe, serializable view of a Go context. It never
// exposes the context object itself, cancellation channels, functions, host
// pointers, or values not listed in fields.
//
// The returned map has these stable keys:
//
//   - values: the explicitly selected context values
//   - hasDeadline: whether ctx carries a deadline
//   - deadlineUnixMilli: the deadline as a Unix millisecond timestamp, or 0
//
// Values are deep-copied through the same bridge used by HostChannel. Only
// primitive values plus supported maps and slices are accepted.
func ContextSnapshot(ctx context.Context, fields ...ContextField) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	values := make(map[string]any, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.Name == "" {
			return nil, errors.New("nanogo: context field name must not be empty")
		}
		if _, duplicate := seen[field.Name]; duplicate {
			return nil, fmt.Errorf("nanogo: duplicate context field %q", field.Name)
		}
		seen[field.Name] = struct{}{}
		if field.Key == nil {
			return nil, fmt.Errorf("nanogo: context field %q has a nil key", field.Name)
		}
		keyType := reflect.TypeOf(field.Key)
		if !keyType.Comparable() {
			return nil, fmt.Errorf("nanogo: context field %q has a non-comparable key", field.Name)
		}

		value, err := copyBridgeValue(ctx.Value(field.Key))
		if err != nil {
			return nil, fmt.Errorf("nanogo: context field %q: %w", field.Name, err)
		}
		values[field.Name] = value
	}

	deadlineUnixMilli := int64(0)
	hasDeadline := false
	if deadline, ok := ctx.Deadline(); ok {
		hasDeadline = true
		deadlineUnixMilli = deadline.UnixMilli()
	}
	return map[string]any{
		"values":            values,
		"hasDeadline":       hasDeadline,
		"deadlineUnixMilli": deadlineUnixMilli,
	}, nil
}

// BindHostContext exposes a snapshot of explicitly selected context metadata
// as a guest global map. Bind it before RunContext, normally with the same
// context (or a child) that will be supplied to RunContext.
//
// RunContext already propagates cancellation and its deadline through the
// evaluator, guest channel operations, and RegisterNativeContext callbacks.
// BindHostContext is for safe, read-only request metadata such as a request
// ID, locale, trace parent, or feature flags.
func (vm *Interpreter) BindHostContext(name string, ctx context.Context, fields ...ContextField) error {
	if !validGuestIdentifier(name) {
		return errors.New("nanogo: host context name must be a Go identifier")
	}
	snapshot, err := ContextSnapshot(ctx, fields...)
	if err != nil {
		return err
	}
	guestValue, err := bridgeToGuest(snapshot)
	if err != nil {
		return fmt.Errorf("nanogo: host context: %w", err)
	}

	vm.runMu.Lock()
	defer vm.runMu.Unlock()
	if vm.execution.Load() != nil {
		return errors.New("nanogo: bind host context before RunContext")
	}
	vm.declare(name, guestValue, vm.globals)
	return nil
}

// copyBridgeValue validates and deep-copies a host value without retaining a
// reference to it. Keeping this at the context boundary is important: context
// values frequently contain request-scoped pointers or credential objects.
func copyBridgeValue(value any) (any, error) {
	guestValue, err := bridgeToGuest(value)
	if err != nil {
		return nil, err
	}
	return bridgeToHost(guestValue)
}
