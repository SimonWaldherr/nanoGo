package interp

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"testing"
)

func packageCallFixture(t testing.TB) (*Interpreter, *PackageScope) {
	t.Helper()
	vm := NewInterpreter()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "calc.go", `package calc
func Combine(a, b, c int) int { return a*100 + b*10 + c }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	ps := vm.NewPackageScope("calc")
	if err := ps.CollectDecls(file, fset); err != nil {
		t.Fatal(err)
	}
	vm.RegisterPackage("calc", ps.Exports())
	return vm, ps
}

func BenchmarkOwnPackageCalls(b *testing.B) {
	vm, _ := packageCallFixture(b)
	const src = `package main
func main() { for i := 0; i < 20000; i++ { _ = calc.Combine(1, 2, 3) } }`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatal(err)
		}
	}
}

func TestOwnPackageCallOrderAndReplacement(t *testing.T) {
	vm, ps := packageCallFixture(t)
	var got []int
	vm.RegisterNative("capture", func(args []any) (any, error) {
		got = append(got, ToInt(args[0]))
		return nil, nil
	})
	const src = `package main
var n = 0
func next() int { n++; return n }
func main() { capture(calc.Combine(next(), next(), next())) }`
	if err := vm.Run(src); err != nil {
		t.Fatal(err)
	}
	// The already-imported object must observe a replacement on the next call.
	ps.Replace("Combine", &Function{Name: "Combine", Native: func(args []any) (any, error) {
		return ToInt(args[0]) + ToInt(args[1]) + ToInt(args[2]), nil
	}})
	if err := vm.Run(src); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 123 || got[1] != 6 {
		t.Fatalf("results: %v", got)
	}
}

func TestOwnPackageCallStepBudget(t *testing.T) {
	for limit := uint64(1); limit < 30; limit++ {
		var counts [2]uint64
		var limited [2]bool
		for mode := 0; mode < 2; mode++ {
			vm, ps := packageCallFixture(t)
			fn, _ := ps.Lookup("Combine")
			// Force the existing full call path as the semantic reference.
			fn.(*Function).frameFree = mode == 0
			vm.Limits.MaxSteps = limit
			err := vm.RunContext(context.Background(), `package main
func main() { _ = calc.Combine(1, 2, 3) }`)
			if err != nil && !errors.Is(err, ErrStepLimit) {
				t.Fatal(err)
			}
			counts[mode], limited[mode] = vm.LastStepCount(), errors.Is(err, ErrStepLimit)
		}
		if counts[0] != counts[1] || limited[0] != limited[1] {
			t.Fatalf("limit %d: steps %v, limited %v", limit, counts, limited)
		}
	}
}

func TestOwnPackageArgumentFailure(t *testing.T) {
	vm, _ := packageCallFixture(t)
	failure := errors.New("argument failed")
	later := false
	vm.RegisterNative("fail", func([]any) (any, error) { return nil, failure })
	vm.RegisterNative("later", func([]any) (any, error) { later = true; return 3, nil })
	err := vm.Run(`package main
func main() { _ = calc.Combine(1, fail(), later()) }`)
	if !errors.Is(err, failure) || later {
		t.Fatalf("err=%v, later argument evaluated=%v", err, later)
	}
}
