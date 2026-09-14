// Package compat compares supported, deterministic programs with the Go
// toolchain. Keep the oracle independent of nanoGo's expected-output tests.
package compat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
)

func TestStandardGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal("Go toolchain required for compatibility tests")
	}
	cases := map[string]string{
		"negative_shifts": `func check(mode int){x:=8;n:=-1;defer func(){fmt.Println(recover()!=nil,x)}();switch mode{case 0:_=x<<n;case 1:_=x>>n;case 2:x<<=n;case 3:x>>=n};fmt.Println("not reached")}
func main(){for i:=0;i<4;i++{check(i)};x:=8;n:=1000;fmt.Println(x<<n,x>>n);x=-8;fmt.Println(x>>n)}`,
		"variadic_sharing": `func change(xs ...int){xs[0]=9}
func main(){xs:=[]int{1,2};change(xs...);fmt.Println(xs[0],xs[1]);defer func(){fmt.Println(xs[0])}();defer change(xs...);xs[0]=3}`,
		"generic_functions": `func Add[T ~int|~float64|~string](a,b T)T{return a+b}
func Zero[T any]()(v T){return}
func Map[T any,U any](values []T,f func(T)U)[]U{result:=make([]U,len(values));for i,v:=range values{result[i]=f(v)};return result}
func main(){fmt.Println(Add(2,3),Add[float64](1.5,2.0),Add("a","b"),Zero[int]());values:=Map([]int{1,2},func(n int)int{return n*2});fmt.Println(values[0],values[1])}`,
		"pointer_locations": `func keep(n int)*int{return &n}
func main(){a:=1;p:=&a;*p=4;fmt.Println(a,p==&a);q:=keep(7);r:=keep(8);fmt.Println(*q,*r,q==r);ps:=[]*int{};for i:=range 3{ps=append(ps,&i)};fmt.Println(*ps[0],*ps[1],*ps[2])}`,
		"channel_pointer": `func replace(p *chan int){*p=make(chan int,1);*p<-7;close(*p)}
func main(){var ch chan int;fmt.Println(ch==nil);replace(&ch);for n:=range ch{fmt.Println(n)};var p *int;fmt.Println(p==nil)}`,
		"flag_set": `import "flag"
func main(){fs:=flag.NewFlagSet("test",flag.ContinueOnError);n:=fs.Int("n",1,"count");v:=fs.Bool("v",false,"verbose");err:=fs.Parse([]string{"-n=7","-v","tail"});fmt.Println(err==nil,*n,*v,fs.NArg(),fs.Arg(0))}`,
		"multiple_results": `var initialN,initialS=named()
func values() (int,string,bool){return 7,"seven",true}
func named() (n int,s string){defer func(){n++;s=s+"!"}();return 7,"seven"}
func main(){fmt.Println(initialN,initialS);a,b,c:=values();fmt.Println(a,b,c);var n,s=named();fmt.Println(n,s);fmt.Println(values())}`,
		"iota_groups": `const(A=1<<iota;B;_;D)
func main(){const(X=iota;Y);fmt.Println(A,B,D,X,Y)}`,
		"range_closures": `func main(){fs:=[]func()int{};for i:=range 3{fs=append(fs,func()int{return i})};for _,f:=range fs{fmt.Println(f())}}`,
		"panic_nil":      `func main(){defer func(){fmt.Println(recover()!=nil)}();panic(nil)}`,
		"initialization": `var n=next(); var _=printValue()
func next() int { return 7 }
func init(){fmt.Println("init",n);n++}
func init(){fmt.Println("second",n)}
func main(){var _=printValue();fmt.Println("main",n)}
func printValue() int {fmt.Println("blank initializer");return 1}`,
		"typed_globals": `var n float64=2
func main(){fmt.Println(n/4);var x float64=3;fmt.Println(x/2)}`,
		"unnamed_receiver": `type T struct{}
func (T) Value() int {return 42}
func main(){v:=T{};fmt.Println(v.Value())}`,
		"closure_variadic": `func makeFn(n int) func() int {return func() int{return n}}
func main(){a:=makeFn(3);b:=makeFn(7);fmt.Println(a(),b());sum:=func(xs ...int) int {n:=0;for _,x:=range xs{n=n+x};return n};fmt.Println(sum(),sum(1,2,3));v:=[]int{4,5};fmt.Println(sum(v...))}`,
		"scope_and_arithmetic": `func main(){x:=7;{x:=3;fmt.Println(x)};fmt.Println(x/2,x%2);a,b:=1,2;a,b=b,a;fmt.Println(a,b);const k=13;fmt.Println(k&7)}`,
		"compound_assignments": `func change(p *int) int {*p=10;return 3}
func main(){x:=7;x+=change(&x);fmt.Println(x);{x:=1.5;x*=2.0;fmt.Println(x)};x-=3;x*=2;x/=4;x%=4;x|=8;x^=3;x&^=1;x<<=2;x>>=1;fmt.Println(x);m:=map[string]int{"n":4};m["n"]+=2;fmt.Println(m["n"]);text:="nano";text+="Go";fmt.Println(text)}`,
		"defer_recover": `func f(){defer func(){fmt.Println(recover())}();panic("caught")}
func main(){defer fmt.Println("last");defer fmt.Println("first");f()}`,
		"buffered_channel": `func main(){ch:=make(chan int,2);ch<-4;ch<-5;close(ch);for n:=range ch{fmt.Println(n)};n,ok:=<-ch;fmt.Println(n,ok);select{case n,ok:=<-ch:fmt.Println(n,ok)};select{case n,ok:=<-ch:fmt.Println(n,ok);default:fmt.Println("unexpected")}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			src := "package main\nimport \"fmt\"\n" + body
			file := filepath.Join(t.TempDir(), "main.go")
			if err := os.WriteFile(file, []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", file)
			want, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("standard Go: %v\n%s", err, want)
			}
			for _, loaded := range []bool{false, true} {
				vm := interp.NewInterpreter()
				interp.RegisterBuiltinPackages(vm)
				var out strings.Builder
				vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
					if len(args) > 0 {
						out.WriteString(interp.ToString(args[0]))
						out.WriteByte('\n')
					}
					return nil, nil
				})
				if loaded {
					if err := vm.VFS.MkdirAll("/app", 0755); err != nil {
						t.Fatal(err)
					}
					if err := vm.VFS.WriteFile("/app/main.go", []byte(src), 0644); err != nil {
						t.Fatal(err)
					}
					p, err := loader.LoadModule(vm.VFS, "/app", loader.Options{ModulePath: "compat.local/app"})
					if err != nil {
						t.Fatal(err)
					}
					err = loader.RunProgram(ctx, vm, p, "main")
					if err != nil {
						t.Fatalf("loader: %v", err)
					}
				} else if err := vm.RunContext(ctx, src); err != nil {
					t.Fatalf("Run: %v", err)
				}
				if out.String() != string(want) {
					t.Fatalf("loaded=%v\nGo: %q\nnanoGo: %q", loaded, want, out.String())
				}
			}
		})
	}
}
