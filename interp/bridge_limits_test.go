package interp

import (
	"errors"
	"reflect"
	"testing"
)

func TestBridgeBoundsAndCopies(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := BridgeToGuest(cycle); !errors.Is(err, ErrBridgeValue) {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := BridgeToGuestWithLimits([]int{1, 2, 3}, BridgeLimits{MaxNodes: 2}); !errors.Is(err, ErrBridgeValue) {
		t.Fatalf("size: %v", err)
	}
	if _, err := BridgeToGuestWithLimits("abcd", BridgeLimits{MaxBytes: 3}); !errors.Is(err, ErrBridgeValue) {
		t.Fatalf("bytes: %v", err)
	}
	original := map[string]any{"data": []int{1, 2}}
	guest, err := BridgeToGuest(original)
	if err != nil {
		t.Fatal(err)
	}
	original["data"].([]int)[0] = 9
	copied, err := BridgeToHost(guest)
	if err != nil {
		t.Fatal(err)
	}
	if copied.(map[string]any)["data"].([]int)[0] != 1 {
		t.Fatal("input alias")
	}
	bad := &SliceVal{ElementType: "any"}
	bad.Data = []any{bad}
	if _, err := BridgeToHost(bad); !errors.Is(err, ErrBridgeValue) {
		t.Fatalf("guest cycle: %v", err)
	}
	if _, err := BridgeToGuest(new(int)); !errors.Is(err, ErrBridgeValue) {
		t.Fatalf("pointer: %v", err)
	}
}

func TestBridgeOverlappingSlicesAndTypedNil(t *testing.T) {
	s := []any{1, nil}
	s[1] = s[:1]
	if _, err := BridgeToGuest(s); err != nil {
		t.Fatalf("acyclic overlap: %v", err)
	}
	for _, value := range []any{[]int(nil), []byte(nil), []string(nil), []any(nil), map[string]any(nil)} {
		guest, err := BridgeToGuest(value)
		if err != nil {
			t.Fatal(err)
		}
		host, err := BridgeToHost(guest)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(host, value) {
			t.Fatalf("%#v -> %#v", value, host)
		}
	}
	m := &MapVal{KeyType: "any", ElementType: "any", Data: map[string]any{}}
	m.setByKey(1, "int")
	m.setByKey("1", "string")
	if _, err := BridgeToHost(m); !errors.Is(err, ErrBridgeValue) {
		t.Fatalf("lossy keys: %v", err)
	}
}
