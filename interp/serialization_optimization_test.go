package interp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestExplicitStringUnicodeConversion(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{65, "A"}, {0x1f30d, "🌍"}, {-1, "�"}, {0xd800, "�"}, {0x110000, "�"},
		{int64(1<<32 + 65), "�"}, {NamedValue{"Code", 65}, "A"},
		{&SliceVal{ElementType: "rune", Data: []any{71, 114, 252, 223, 101, 32, 0x1f30d}}, "Grüße 🌍"},
		{&SliceVal{ElementType: "int32", Data: []any{-1, 0xd800, 0x110000}}, "���"},
		{&SliceVal{ElementType: "rune"}, ""},
	} {
		if got := builtinConvert("string", tc.value); got != tc.want {
			t.Errorf("%#v: got %q, want %q", tc.value, got, tc.want)
		}
	}
	if ToString(65) != "65" {
		t.Fatal("console formatting must remain decimal")
	}
	data := &SliceVal{ElementType: "byte", Data: []any{65, 0, 255}}
	text := builtinConvert("string", data).(string)
	data.Data[0] = 66
	if text != "A\x00\xff" {
		t.Fatalf("conversion aliases slice: %q", text)
	}
	legacy := &SliceVal{ElementType: "byte", Data: []any{int64(65), NamedValue{"Octet", 66}, -1}}
	if got := ToString(legacy); got != "AB\xff" {
		t.Fatalf("host formatting fallback: %q", got)
	}
}

func TestJSONScalarConversionAndSliceReuse(t *testing.T) {
	vm := NewInterpreter()
	for _, value := range []any{nil, true, "Grüße", 42, -1, 1.25, []any{true, nil, 42, "x"}, map[string]any{"n": 42, "s": "text"}} {
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := vm.marshalJSON(value)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("marshal(%#v): %s, %v; want %s", value, got, err, want)
		}
	}
	backing := []any{10, 20, 30, 40}
	slice := &SliceVal{ElementType: "int", Data: backing[:2]}
	p := newPointerValue(slice, "[]int")
	if err := vm.unmarshalJSON([]byte(`[1,2,3]`), p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backing, []any{1, 2, 3, 40}) {
		t.Fatalf("lost backing aliases: %#v", backing)
	}
	if err := vm.unmarshalJSON([]byte(`[9,"bad",7]`), p); err == nil {
		t.Fatal("type error accepted")
	}
	if !reflect.DeepEqual(backing, []any{9, 2, 7, 40}) {
		t.Fatalf("partial updates differ: %#v", backing)
	}
	// Reusing host storage must not bypass the documented guest budget.
	vm.Limits.MaxAllocationUnits = 2
	err := vm.WithExecution(context.Background(), nil, func() error {
		return vm.unmarshalJSON([]byte(`[4,5,6]`), p)
	})
	if !errors.Is(err, ErrAllocationLimit) {
		t.Fatalf("reused slice budget: %v", err)
	}
	if !reflect.DeepEqual(backing, []any{9, 2, 7, 40}) {
		t.Fatalf("budget failure mutated target: %#v", backing)
	}
}

func TestByteStringBufferBoundaries(t *testing.T) {
	for _, n := range []int{0, 16, 32, 33, 4096} {
		want := make([]byte, n)
		data := make([]any, n)
		for i := range data {
			want[i] = byte(i * 17)
			data[i] = int(want[i])
			if i%7 == 0 {
				data[i] = int64(want[i])
			}
		}
		got := ToString(&SliceVal{ElementType: "byte", Data: data})
		clear(data)
		if got != string(want) {
			t.Fatalf("%d bytes: copied contents differ", n)
		}
	}
}

func BenchmarkJSONScalarMarshal(b *testing.B) {
	vm := NewInterpreter()
	for _, value := range []any{42, "nanoGo", true} {
		b.Run(fmt.Sprintf("%T", value), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := vm.marshalJSON(value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkJSONReuseSlice(b *testing.B) {
	vm := NewInterpreter()
	data := []byte("[" + strings.Repeat("1,", 255) + "1]")
	p := newPointerValue(&SliceVal{ElementType: "int", Data: make([]any, 256)}, "[]int")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.unmarshalJSON(data, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkByteStringConversion(b *testing.B) {
	for _, n := range []int{16, 4096, 65536} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			value := byteSliceValue(bytes.Repeat([]byte{'x'}, n))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if len(ToString(value)) != n {
					b.Fatal("length mismatch")
				}
			}
		})
	}
}
