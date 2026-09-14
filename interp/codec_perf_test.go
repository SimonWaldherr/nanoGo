package interp

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
)

var codecBenchmarkSink any

func TestCodecBufferWriteOwnership(t *testing.T) {
	for _, size := range []int{0, 1, 64, 8192} {
		original := bytes.Repeat([]byte{0, 127, 128, 255}, size)
		for _, input := range []any{string(original), original, byteSliceValue(original)} {
			var buf bytes.Buffer
			buf.WriteString("prefix")
			n, err := writeBufferValue(&buf, input)
			if err != nil || n != len(original) || !bytes.Equal(buf.Bytes()[6:], original) {
				t.Fatalf("write %T: n=%d err=%v", input, n, err)
			}
			if guest, ok := input.(*SliceVal); ok && len(guest.Data) > 0 {
				guest.Data[0] = 99
				if buf.Bytes()[6] != 0 {
					t.Fatal("writer retained guest backing storage")
				}
			}
			buf.Reset()
			if _, err := writeBufferValue(&buf, "reused"); err != nil || buf.String() != "reused" {
				t.Fatal("buffer reuse failed")
			}
		}
	}
}

func TestCodecGobIndependentStreams(t *testing.T) {
	vm := NewInterpreter()
	registerGobPackage(vm)
	encode := vm.packages["encoding/gob"].Funcs["Encode"].Native
	decode := vm.packages["encoding/gob"].Funcs["Decode"].Native
	for _, want := range []any{"first", 12345, strings.Repeat("x", 8192), "last"} {
		wire, err := encode([]any{want})
		if err != nil {
			t.Fatal(err)
		}
		data := binaryArg(wire)
		var native gobEnvelope
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&native); err != nil || native.Value != want {
			t.Fatalf("standard gob decoder: %v, %v", native.Value, err)
		}
		for _, input := range []any{wire, data, string(data)} {
			got, err := decode([]any{input})
			if err != nil || got != want {
				t.Fatalf("decode %T: %v, %v", input, got, err)
			}
		}
	}
	if _, err := decode([]any{"broken"}); err == nil {
		t.Fatal("malformed gob accepted")
	}
}

func TestCodecGobPooledOutputIsolation(t *testing.T) {
	vm := NewInterpreter()
	registerGobPackage(vm)
	encode := vm.packages["encoding/gob"].Funcs["Encode"].Native
	decode := vm.packages["encoding/gob"].Funcs["Decode"].Native
	for i := 0; i < 8; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			var saved []any
			values := []any{"keep", strings.Repeat("large", maxPooledGobBuffer), "after-large"}
			for _, value := range values {
				wire, err := encode([]any{value})
				if err != nil {
					t.Fatal(err)
				}
				saved = append(saved, wire)
			}
			if _, err := encode([]any{make(chan int)}); err == nil {
				t.Fatal("unsupported gob value accepted")
			}
			for j, wire := range saved {
				got, err := decode([]any{wire})
				if err != nil || got != values[j] {
					t.Fatalf("pooled output changed: %v", err)
				}
			}
		})
	}
}

func TestCodecHashRepresentation(t *testing.T) {
	for _, value := range []int64{math.MinInt64, -123456789, -1, 0, 1, math.MaxInt64} {
		if got, want := hashKey(value), "I:"+strconv.FormatInt(value, 10); got != want {
			t.Fatalf("%q != %q", got, want)
		}
		if got, want := hashKey(int(value)), "i:"+strconv.Itoa(int(value)); got != want {
			t.Fatalf("%q != %q", got, want)
		}
	}
	for _, value := range []float64{math.SmallestNonzeroFloat64, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(), math.Copysign(0, -1)} {
		if got, want := hashKey(value), "f:"+strconv.FormatFloat(value, 'g', -1, 64); got != want {
			t.Fatalf("%q != %q", got, want)
		}
	}
}

func TestCodecReaderBounds(t *testing.T) {
	for _, size := range []int{0, 1, 8192} {
		payload := strings.Repeat("x", size)
		readers := []io.Reader{strings.NewReader(payload), bytes.NewReader([]byte(payload)), bytes.NewBufferString(payload), misleadingLenReader{r: strings.NewReader(payload), hint: 1}}
		for _, reader := range readers {
			got, err := readWithContext(context.Background(), reader, int64(size))
			if err != nil || string(got) != payload {
				t.Fatalf("%T: len=%d err=%v", reader, len(got), err)
			}
		}
	}
	if got, err := readWithContext(context.Background(), strings.NewReader("ok"), math.MaxInt64); err != nil || string(got) != "ok" {
		t.Fatalf("MaxInt64 limit: %q %v", got, err)
	}
	if _, err := readWithContext(context.Background(), strings.NewReader("long"), 3); err == nil {
		t.Fatal("limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := strings.NewReader("untouched")
	if _, err := readWithContext(ctx, r, 100); !errors.Is(err, context.Canceled) || r.Len() != 9 {
		t.Fatal("canceled read consumed input")
	}
}

func BenchmarkCodecBufferWrite(b *testing.B) {
	vm := NewInterpreter()
	registerBytesPackage(vm)
	write := vm.types["Buffer"].Methods["Write"].Native
	for name, input := range map[string]any{
		"string":     strings.Repeat("x", 8192),
		"guestBytes": byteSliceValue(bytes.Repeat([]byte("x"), 8192)),
	} {
		b.Run(name, func(b *testing.B) {
			value := &StructVal{TypeName: "Buffer"}
			buf := ensureNativeBuffer(value)
			buf.Grow(8192)
			args := []any{value, input}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Reset()
				if _, err := write(args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCodecGobDecodeString(b *testing.B) {
	vm := NewInterpreter()
	registerGobPackage(vm)
	var wire bytes.Buffer
	if err := gob.NewEncoder(&wire).Encode(gobEnvelope{Value: strings.Repeat("x", 8192)}); err != nil {
		b.Fatal(err)
	}
	decode := vm.packages["encoding/gob"].Funcs["Decode"].Native
	args := []any{wire.String()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		codecBenchmarkSink, err = decode(args)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecGobEncode(b *testing.B) {
	vm := NewInterpreter()
	registerGobPackage(vm)
	encode := vm.packages["encoding/gob"].Funcs["Encode"].Native
	args := []any{strings.Repeat("x", 8192)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		codecBenchmarkSink, err = encode(args)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecHash(b *testing.B) {
	for name, value := range map[string]any{"int": 123456789, "int64": int64(-123456789), "float": 12345.6789} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				codecBenchmarkSink = hashKey(value)
			}
		})
	}
}

func BenchmarkCodecVFSInit(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		codecBenchmarkSink = NewVFS()
	}
}

func BenchmarkCodecReader(b *testing.B) {
	payload := strings.Repeat("x", 8192)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		data, err := readWithContext(context.Background(), strings.NewReader(payload), int64(len(payload)))
		if err != nil {
			b.Fatal(err)
		}
		codecBenchmarkSink = data
	}
}
