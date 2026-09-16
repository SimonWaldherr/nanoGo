package interp

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
)

// ErrBridgeValue identifies unsupported, cyclic or over-budget boundary values.
var ErrBridgeValue = errors.New("nanogo: invalid bridge value")

// BridgeLimits bounds a copied value before allocating its destination. Zero
// fields select defaults (64 levels, 1M values, 16MiB scalar/key bytes). Bytes
// count UTF-8 strings/keys and eight bytes per numeric value, not Go heap size.
type BridgeLimits struct {
	MaxDepth int
	MaxNodes uint64
	MaxBytes uint64
}

func (l BridgeLimits) defaults() BridgeLimits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = 64
	}
	if l.MaxNodes == 0 {
		l.MaxNodes = 1 << 20
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = 16 << 20
	}
	return l
}

type bridgeVisit struct {
	kind    reflect.Kind
	pointer uintptr
	length  int
}
type bridgeValidator struct {
	limits       BridgeLimits
	nodes, bytes uint64
	active       map[bridgeVisit]bool
}

func validateBridge(value any, limits BridgeLimits, guest bool) error {
	v := bridgeValidator{limits: limits.defaults(), active: map[bridgeVisit]bool{}}
	return v.walk(value, 1, guest)
}
func (v *bridgeValidator) addBytes(n uint64) error {
	if n > v.limits.MaxBytes-v.bytes {
		return fmt.Errorf("%w: byte budget exceeded", ErrBridgeValue)
	}
	v.bytes += n
	return nil
}
func (v *bridgeValidator) walk(value any, depth int, guest bool) error {
	if depth > v.limits.MaxDepth {
		return fmt.Errorf("%w: nesting depth exceeded", ErrBridgeValue)
	}
	if v.nodes >= v.limits.MaxNodes {
		return fmt.Errorf("%w: value count exceeded", ErrBridgeValue)
	}
	v.nodes++
	switch x := value.(type) {
	case NamedValue:
		if guest {
			return v.walk(x.Value, depth+1, guest)
		}
	case nil:
		return nil
	case bool:
		return v.addBytes(1)
	case string:
		return v.addBytes(uint64(len(x)))
	case int, int64:
		return v.addBytes(8)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("%w: non-finite number", ErrBridgeValue)
		}
		return v.addBytes(8)
	}
	rv := reflect.ValueOf(value)
	var key bridgeVisit
	switch rv.Kind() {
	case reflect.Map:
		key = bridgeVisit{kind: rv.Kind(), pointer: uintptr(rv.UnsafePointer())}
	case reflect.Slice, reflect.Pointer:
		key = bridgeVisit{kind: rv.Kind(), pointer: rv.Pointer()}
		if rv.Kind() == reflect.Slice {
			key.length = rv.Len()
		}
	}
	if key.pointer != 0 {
		if v.active[key] {
			return fmt.Errorf("%w: cycle", ErrBridgeValue)
		}
		v.active[key] = true
		defer delete(v.active, key)
	}
	walk := func(x any) error { return v.walk(x, depth+1, guest) }
	if guest {
		switch x := value.(type) {
		case *SliceVal:
			if x == nil {
				return nil
			}
			for _, item := range x.Data {
				if err := walk(item); err != nil {
					return err
				}
			}
			return nil
		case *MapVal:
			if x != nil && x.KeyType != "string" {
				return fmt.Errorf("%w: guest maps require string keys", ErrBridgeValue)
			}
			if x == nil {
				return nil
			}
			for k, item := range x.Data {
				if err := v.addBytes(uint64(len(k))); err != nil {
					return err
				}
				if err := walk(item); err != nil {
					return err
				}
			}
			return nil
		case *StructVal:
			if x == nil {
				return nil
			}
			var err error
			x.forEachField(func(k string, item any) {
				if err != nil || strings.HasPrefix(k, "__") {
					return
				}
				err = v.addBytes(uint64(len(k)))
				if err == nil {
					err = walk(item)
				}
			})
			return err
		}
	} else {
		switch value.(type) {
		case []byte, []any, []string, []int, []float64, []bool:
			if uint64(rv.Len()) > v.limits.MaxNodes-v.nodes {
				return fmt.Errorf("%w: value count exceeded", ErrBridgeValue)
			}
			for i := 0; i < rv.Len(); i++ {
				if rv.Type().Elem().Kind() == reflect.Uint8 {
					v.nodes++
					if err := v.addBytes(1); err != nil {
						return err
					}
				} else if err := walk(rv.Index(i).Interface()); err != nil {
					return err
				}
			}
			return nil
		case map[string]any, map[string]string, map[string]int:
			if uint64(rv.Len()) > v.limits.MaxNodes-v.nodes {
				return fmt.Errorf("%w: value count exceeded", ErrBridgeValue)
			}
			iter := rv.MapRange()
			for iter.Next() {
				if err := v.addBytes(uint64(len(iter.Key().String()))); err != nil {
					return err
				}
				if err := walk(iter.Value().Interface()); err != nil {
					return err
				}
			}
			return nil
		}
	}
	side := "host"
	if guest {
		side = "guest"
	}
	return fmt.Errorf("%w: unsupported %s channel value %T", ErrBridgeValue, side, value)
}

// BridgeToGuestWithLimits validates and copies supported host data; it never
// exposes arbitrary host pointers. The caller must not mutate data concurrently.
func BridgeToGuestWithLimits(value any, limits BridgeLimits) (any, error) {
	if err := validateBridge(value, limits, false); err != nil {
		return nil, err
	}
	return bridgeToGuestUnchecked(value)
}

// BridgeToHostWithLimits copies a guest value into ordinary Go data.
func BridgeToHostWithLimits(value any, limits BridgeLimits) (any, error) {
	if err := validateBridge(value, limits, true); err != nil {
		return nil, err
	}
	return bridgeToHostUnchecked(value)
}
func bridgeToGuest(value any) (any, error) { return BridgeToGuestWithLimits(value, BridgeLimits{}) }
func bridgeToHost(value any) (any, error)  { return BridgeToHostWithLimits(value, BridgeLimits{}) }
