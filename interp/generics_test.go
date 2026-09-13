package interp

import (
	"go/parser"
	"go/types"
	"strings"
	"testing"
)

func TestGenericTypeExprFastPath(t *testing.T) {
	for _, name := range []string{"int", "int64", "float64", "string", "bool", "byte", "rune", "any", "error", "Missing", "true", "nil", "[]int", "map[string]int", "*int", "func(int) string"} {
		t.Run(name, func(t *testing.T) {
			expr, err := parser.ParseExpr(name)
			if err != nil {
				t.Fatal(err)
			}
			got, gotErr := genericTypeExpr(expr)
			// Parenthesized expressions use the original formatting path.
			wrapped, err := parser.ParseExpr("(" + name + ")")
			if err != nil {
				t.Fatal(err)
			}
			want, wantErr := genericTypeExpr(wrapped)
			if (gotErr == nil) != (wantErr == nil) || (gotErr == nil && !types.Identical(got, want)) {
				t.Fatalf("direct: %v, %v; formatted: %v, %v", got, gotErr, want, wantErr)
			}
		})
	}
}

func TestGenericManyTypeArguments(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func Pick[A,B,C,D,E any](a A,b B,c C,d D,e E)E{return e}
func main(){fmt.Println(Pick[int,string,bool,float64,int](1,"x",true,2.0,3),Pick[int,string,bool,float64,string](1,"x",true,2.0,"last"))}`)
	if out != "3 last\n" {
		t.Fatalf("got %q", out)
	}
}

func TestGenericFunctions(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func Identity[T any](value T)T{return value}
func Add[T ~int|~float64|~string](a,b T)T{return a+b}
func Zero[T any]()(value T){return}
func Pair[A any,B any](a A,b B)(A,B){return a,b}
func Sum[T ~int|~float64](values ...T)T{var total T;for _,v:=range values{total+=v};return total}
func Pointer[T any](value T)*T{return &value}
func main(){
 fmt.Println(Identity[int](7),Identity("seven"))
 fmt.Println(Add(2,3),Add[float64](1.5,2.5),Add("a","b"))
 fmt.Println(Zero[int](),Zero[string]()=="")
 a,b:=Pair[int,string](3,"three");fmt.Println(a,b)
 fmt.Println(Sum(1,2,3),Sum([]int{4,5}...))
 fmt.Println(*Pointer(8))
 saved:=Identity[string];fmt.Println(saved("saved"))
}`)
	want := "7 seven\n5 4 ab\n0 true\n3 three\n6 9\n8\nsaved\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestGenericContainersAndCallbacks(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func Map[T any,U any](values []T,f func(T)U)[]U{result:=make([]U,len(values));for i,value:=range values{result[i]=f(value)};return result}
func Lookup[K comparable,V any](values map[K]V,key K)(V,bool){v,ok:=values[key];return v,ok}
func main(){result:=Map([]int{1,2},func(n int)string{return "ok"});fmt.Println(result[0],result[1]);v,ok:=Lookup(map[string]int{"x":7},"x");fmt.Println(v,ok)}`)
	if out != "ok ok\n7 true\n" {
		t.Fatalf("got %q", out)
	}
}

func TestGenericConstraintsRejectInvalidCalls(t *testing.T) {
	for name, body := range map[string]string{
		"constraint":         `_ = Add[string]("a","b")`,
		"inference_conflict": `_ = Add(1,"bad")`,
		"explicit_argument":  `_ = Add[int]("a","b")`,
		"argument_count":     `_ = Add[int](1)`,
		"type_count":         `_ = Add[int,string](1,2)`,
	} {
		t.Run(name, func(t *testing.T) {
			vm, _ := newTestVM()
			err := vm.Run(`package main
func Add[T ~int|~float64](a,b T)T{return a+b}
func main(){` + body + `}`)
			if err == nil {
				t.Fatal("invalid generic call accepted")
			}
		})
	}
}

func TestGenericShadowedTypeName(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func Shadow[T any](value T)T{{T:=1;_ = T};return value}
func main(){fmt.Println(Shadow("kept"))}`)
	if out != "kept\n" {
		t.Fatalf("got %q", out)
	}
}

func TestGenericUnsupportedDependencyIsExplicit(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main
func helper()int{return 1}
func Use[T any](v T)T{helper();return v}
func main(){_=Use(1)}`)
	if err == nil || !strings.Contains(err.Error(), "unsupported or invalid declaration") {
		t.Fatalf("got %v", err)
	}
}

func TestGenericConcurrentInstantiation(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func Identity[T any](v T)T{return v}
func main(){results:=make(chan int,16);for i:=0;i<16;i++{go func(){results<-Identity[int](7)}()};total:=0;for i:=0;i<16;i++{total+=<-results};fmt.Println(total)}`)
	if out != "112\n" {
		t.Fatalf("got %q", out)
	}
}

func TestGenericCacheAndNativeTypeValidation(t *testing.T) {
	vm, _ := newTestVM()
	if err := vm.Run(`package main
func Identity[T any](v T)T{return v}
func main(){a:=Identity[int];b:=Identity[int];_=a(1);_=b(2)}`); err != nil {
		t.Fatal(err)
	}
	fn := vm.funcs["Identity"]
	if fn == nil || len(fn.generic.instances) != 1 {
		t.Fatal("specializations were not reused")
	}
}

func BenchmarkGenericCachedCall(b *testing.B) {
	vm, _ := newTestVM()
	const src = `package main
func Add[T ~int](a,b T)T{return a+b}
func main(){f:=Add[int];for i:=0;i<1000;i++{_=f(i,1)}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenericExplicitCall(b *testing.B) {
	vm, _ := newTestVM()
	const src = `package main
func Add[T ~int](a,b T)T{return a+b}
func main(){for i:=0;i<1000;i++{_=Add[int](i,1)}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatal(err)
		}
	}
}
