package interp

import (
	"fmt"
	"reflect"
	"testing"
)

func BenchmarkVMLiterals(b *testing.B) {
	for _, tc := range []struct{ name, body string }{
		{"EscapedString", `for i := 0; i < 2000; i++ { _ = "line\nGr\u00fc\u00dfe\tworld" }`},
		{"PlainString", `for i := 0; i < 2000; i++ { _ = "plain text" }`},
		{"Rune", `for i := 0; i < 2000; i++ { _ = '\u754c' }`},
		{"MapCounter", `m := map[string]int{}; for i := 0; i < 2000; i++ { m["line\nkey"] = m["line\nkey"] + 1 }`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			vm, _ := newTestVM()
			src := "package main\nfunc main(){" + tc.body + "}"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := vm.Run(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Compare the same guest program with and without the immutable value cache.
// Clearing it through a host callback also exercises the fallback used for
// package-loader and hot-swapped ASTs that do not belong to Run's parsed file.
func TestVMLiteralCachePreservesExecution(t *testing.T) {
	const src = `package main
func main() {
 configure()
 m := map[string]int{}
 for i := 0; i < 10; i++ {
  m["line\nkey"] = m["line\nkey"] + 1
  m["tab\tkey"]++
 }
 capture("Gr\u00fc\u00dfe\n", '\u754c', '\n', "", "\x00", m["line\nkey"], m["tab\tkey"])
}`
	for _, limit := range []uint64{0, 25, 100, 500, 2000} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var outputs [2][]any
			var errors [2]string
			var steps [2]uint64
			for mode := 0; mode < 2; mode++ {
				vm, _ := newTestVM()
				vm.Limits.MaxSteps = limit
				vm.RegisterNative("configure", func([]any) (any, error) {
					if mode == 1 {
						vm.activeExecution.valueLitCache = nil
					}
					return nil, nil
				})
				vm.RegisterNative("capture", func(args []any) (any, error) { outputs[mode] = append([]any(nil), args...); return nil, nil })
				if err := vm.Run(src); err != nil {
					errors[mode] = err.Error()
				}
				steps[mode] = vm.LastStepCount()
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) || errors[0] != errors[1] || steps[0] != steps[1] {
				t.Fatalf("cached/uncached mismatch: output=%#v errors=%v steps=%v", outputs, errors, steps)
			}
			if limit == 0 {
				want := []any{"Grüße\n", int('界'), int('\n'), "", "\x00", 10, 10}
				if !reflect.DeepEqual(outputs[0], want) {
					t.Fatalf("got %#v want %#v", outputs[0], want)
				}
			}
		})
	}
}

func TestVMLiteralCacheGuestGoroutines(t *testing.T) {
	vm, _ := newTestVM()
	vm.RegisterNative("capture", func(args []any) (any, error) {
		if !reflect.DeepEqual(args, []any{"Grüße\n", int('界')}) {
			return nil, fmt.Errorf("incorrect literals: %#v", args)
		}
		return nil, nil
	})
	if err := vm.Run(`package main
func main() {
 done := make(chan int, 4)
 for i := 0; i < 4; i++ {
  go func() {
   for j := 0; j < 100; j++ { capture("Gr\u00fc\u00dfe\n", '\u754c') }
   done <- 1
  }()
 }
 for i := 0; i < 4; i++ { <-done }
}`); err != nil {
		t.Fatal(err)
	}
}
