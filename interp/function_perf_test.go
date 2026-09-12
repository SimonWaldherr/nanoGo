package interp

import (
	"errors"
	"strings"
	"testing"
)

func TestVariadicFastCallStepLimits(t *testing.T) {
	for limit := uint64(1); limit < 45; limit++ {
		var steps [2]uint64
		var limited [2]bool
		for mode := 0; mode < 2; mode++ {
			vm := NewInterpreter()
			if mode == 1 {
				vm.SetTracer(NewTracer(8))
			}
			vm.Limits.MaxSteps = limit
			err := vm.Run(`package main; func first(xs ...int) int {return xs[0]}; func main(){var x=12345; _=first(x)}`)
			if err != nil && !errors.Is(err, ErrStepLimit) {
				t.Fatal(err)
			}
			steps[mode] = vm.LastStepCount()
			limited[mode] = errors.Is(err, ErrStepLimit)
		}
		if steps[0] != steps[1] || limited[0] != limited[1] {
			t.Fatalf("limit %d: steps %v, limited %v", limit, steps, limited)
		}
	}
}

func TestVariadicClosureLifetime(t *testing.T) {
	for _, traced := range []bool{false, true} {
		vm, out := newTestVM()
		if traced {
			vm.SetTracer(NewTracer(32))
		}
		err := vm.Run(`package main
func keep(xs ...int) func() int { return func() int { return xs[0] } }
func retained(xs ...int) []int { return xs }
func main() {
 f := func(base int, xs ...int) int { for _, x := range xs { base = base+x }; return base }
 ConsoleLog(f(10))
 ConsoleLog(f(10, 1, 2, 3))
 values := []int{4,5}
 ConsoleLog(f(10, values...))
 a := keep(1234); b := keep(5678)
 ConsoleLog(a()); ConsoleLog(b())
 first := retained(11,12); second := retained(21,22)
 ConsoleLog(first[0]); ConsoleLog(second[0])
 fs := []func() int{}
 for i:=0; i<3; i++ { var n=12345; n=n+i; fs=append(fs,func() int { return n }) }
 ConsoleLog(fs[0]()); ConsoleLog(fs[2]())
}`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out.String()); got != "10\n16\n19\n1234\n5678\n11\n21\n12345\n12347" {
			t.Fatalf("traced=%v: %s", traced, got)
		}
	}
}

func TestNumericDeclarationsPreserveScopeAndConversion(t *testing.T) {
	got := runAndCapture(t, `package main
var global=12345
const scale=1.25
func main(){
 var x int=0x1234
 const y float64=1.25
 var converted float64=2
 { var global=67890; ConsoleLog(global) }
 ConsoleLog(global); ConsoleLog(scale); ConsoleLog(x); ConsoleLog(y); ConsoleLog(converted)
}`)
	if strings.TrimSpace(got) != "67890\n12345\n1.25\n4660\n1.25\n2" {
		t.Fatal(got)
	}
}

func BenchmarkFunctionSetup(b *testing.B) {
	for name, src := range map[string]string{
		"Init":         `package main; func main(){}`,
		"Anonymous":    `package main; func main(){ for i:=0;i<5000;i++ { f:=func(a,b,c int) int { return a+b+c }; _=f(1,2,3) } }`,
		"Variadic":     `package main; func sum(a int, xs ...int) int { for _,x:=range xs { a=a+x }; return a }; func main(){for i:=0;i<5000;i++ { _=sum(1,2,3,4,5) }}`,
		"Declarations": `package main; func main(){for i:=0;i<5000;i++ {var a=12345; const b=1.25; var c=12346; _=a; _=b; _=c }}`,
	} {
		b.Run(name, func(b *testing.B) {
			vm := NewInterpreter()
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
