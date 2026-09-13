package interp

import "testing"

func TestMultipleReturnValues(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func pair() (int, string) { return 7, "seven" }
func triple() (int, int, int) { return 1, 2, 3 }
func forward() (int, string) { return pair() }
func named() (n int, s string) { defer func(){ n++; s=s+"!" }(); return pair() }
func zero() (n int, s string, ok bool) { return }
func caught() (n int, ok bool) { defer func(){ if recover()!=nil { n=9; ok=true } }(); panic("x") }
func sum(a,b,c int) int { return a+b+c }
func main() {
 a,b:=pair(); fmt.Println(a,b)
 a,b=forward(); fmt.Println(a,b)
 var c,d=pair(); fmt.Println(c,d)
 x,y,z:=triple(); fmt.Println(x,y,z,sum(triple()))
 fmt.Println(named())
 fmt.Println(zero())
 fmt.Println(caught())
}`)
	want := "7 seven\n7 seven\n7 seven\n1 2 3 6\n8 seven!\n0  false\n9 true\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestMultipleAssignmentDoesNotSwallowPanic(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func broken() (int,error) { panic("caught") }
func main() { defer func(){ fmt.Println(recover()) }(); a,err:=broken(); fmt.Println("unreachable",a,err) }`)
	if out != "caught\n" {
		t.Fatalf("got %q", out)
	}
}

func TestMultipleReturnSideEffects(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
var n=0
func next() int { n++; return n }
func values() (int,int,int) { return next(),next(),next() }
func main(){ a,b,c:=values(); fmt.Println(a,b,c,n) }`)
	if out != "1 2 3 3\n" {
		t.Fatalf("got %q", out)
	}
}

func TestConstantIotaGroups(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
const ( A=1<<iota; B; _; D; E,F=iota,iota+10; G,H )
func main(){const ( X=iota; Y; Z=Y+10 ); fmt.Println(A,B,D,E,F,G,H,X,Y,Z) }`)
	if out != "1 2 8 4 14 5 15 0 1 11\n" {
		t.Fatalf("got %q", out)
	}
}

func TestPanicNilIsRecoverable(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func f(){defer func(){fmt.Println(recover()!=nil)}();panic(nil)}
func g(){defer func(){fmt.Println(recover()!=nil)}();defer panic(nil)}
func main(){f();g()}`)
	if out != "true\ntrue\n" {
		t.Fatalf("got %q", out)
	}
}

func TestRangeClosureBindings(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func main(){
 funcs:=[]func() int{}
 for i:=range 3 { funcs=append(funcs,func() int{return i}) }
 for _,f:=range funcs { fmt.Println(f()) }
 ch:=make(chan int,2); ch<-7;ch<-8;close(ch)
 funcs=[]func() int{}
 for value:=range ch { funcs=append(funcs,func() int{return value}) }
 for _,f:=range funcs { fmt.Println(f()) }
}`)
	if out != "0\n1\n2\n7\n8\n" {
		t.Fatalf("got %q", out)
	}
}

func TestVariadicSpreadAliasingAndDefer(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func change(xs ...int) { xs[0]=9 }
func later(xs ...int) { fmt.Println(xs[0],len(xs)) }
func main(){xs:=[]int{1,2};change(xs...);fmt.Println(xs[0]);defer later(xs...);defer fmt.Println([]interface{}{"deferred",2}...);xs[0]=7}`)
	if out != "9\ndeferred 2\n7 2\n" {
		t.Fatalf("got %q", out)
	}
}

func TestEmbeddedInterfaceRequirements(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
type Combined interface { Base; Extra() int }
type Base interface { Value() int }
type Complete struct{}
func (Complete) Value() int{return 1}
func (Complete) Extra() int{return 2}
type Incomplete struct{}
func (Incomplete) Extra() int{return 2}
func main(){var a any=Complete{};_,ok:=a.(Combined);fmt.Println(ok);var b any=Incomplete{};_,ok=b.(Combined);fmt.Println(ok);_,ok=b.(interface{Base});fmt.Println(ok)}`)
	if out != "true\nfalse\nfalse\n" {
		t.Fatalf("got %q", out)
	}
}

func TestModuloSignsAndRuntimeCount(t *testing.T) {
	out := runAndCapture(t, `package main
import ("fmt"; rt "runtime")
func main(){a,b:=7,3;fmt.Println(a%b,-a%b,a%(-b),(-a)%(-b));fmt.Println(rt.NumGoroutine()>=1)}`)
	if out != "1 -1 1 -1\ntrue\n" {
		t.Fatalf("got %q", out)
	}
}

func TestHostReturnValuesAndBlankResults(t *testing.T) {
	vm, out := newTestVM()
	vm.RegisterNative("hostValues", func([]any) (any, error) { return ReturnValues{3, "host", true}, nil })
	err := vm.Run(`package main
import "fmt"
func blank() (_ int, _ int){return 4,5}
func main(){a,b,c:=hostValues();fmt.Println(a,b,c);fmt.Println(blank())}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "3 host true\n4 5\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestNamedResultShadowAndNilSpread(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func shadow()(n int){if true{n:=8;return n};return}
func size(xs ...int)int{return len(xs)}
func main(){fmt.Println(shadow(),size(nil...));defer fmt.Println([]interface{}{}...);defer func(xs ...int){fmt.Println(len(xs))}(nil...)}`)
	if out != "8 0\n0\n\n" {
		t.Fatalf("got %q", out)
	}
}

func BenchmarkVariadicSpread(b *testing.B) {
	vm, _ := newTestVM()
	const src = `package main
func first(xs ...int) int{return xs[0]}
func main(){xs:=make([]int,64);for i:=0;i<1000;i++{_=first(xs...)}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatal(err)
		}
	}
}
