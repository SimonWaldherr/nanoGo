// Package compat compares supported, deterministic programs with the Go
// toolchain. Keep the oracle independent of nanoGo's expected-output tests.
package compat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		"explicit_string_conversions": `type Code int32
func main(){
fmt.Println(string(65),string(0x1F30D),string(-1),string(0xD800),string(0x110000))
r:=[]rune{71,114,252,223,101,32,0x1F30D};fmt.Println(string(r))
bad:=[]rune{-1,0xD800,0x110000};fmt.Println(string(bad))
var empty []rune;fmt.Println(len(string(empty)))
b:=[]byte{65,0,255};s:=string(b);b[0]=66;fmt.Println(len(s),s[0],s[1],s[2])
fmt.Println(string(Code(65)))
}`,
		"integer_argument_boundaries": `type Signed int64
type Small int8
type Octet uint8
type Derived Small
type Alias = Signed
const largest = ` + strconv.Itoa(int(^uint(0)>>1)) + `
const smallest = -largest-1
func signed(n int64){fmt.Println(n)}
func named(n Signed){fmt.Println(n)}
func alias(n Alias){fmt.Println(n)}
func narrow(n Small,b Octet,d Derived){fmt.Println(n,b,d)}
func main(){signed(largest);signed(smallest);named(largest);named(smallest);alias(largest);narrow(-128,255,127)}`,
		"float32_argument_rounding": `type Single float32
func plain(n float32)bool{return float64(n)>3.4028234e38 && float64(n)<3.4028236e38}
func named(n Single)bool{return float64(n)>3.4028234e38 && float64(n)<3.4028236e38}
func main(){fmt.Println(plain(3.4028235e38),named(3.4028235e38))}`,
		"unicode_slice_conversions": `func main(){b:=[]byte("Grüße 🌍");fmt.Println(len(b),string(b),b[3]);r:=[]rune("Grüße 🌍");fmt.Println(len(r),r[2],r[6]);var n=[]byte(nil);fmt.Println(n==nil,len(n))}`,
		"aliases_and_named_scalars": `type Count int
type Alias = Count
type Ordinary = int
func take(n Count){fmt.Println(n)}
func main(){var c Count;take(3);c=Count(4);var x any=c;_,a:=x.(Count);_,b:=x.(Alias);_,d:=x.(int);fmt.Println(a,b,d);x=c+1;_,a=x.(Count);fmt.Println(a);var z Ordinary;var y any=z;_,a=y.(int);fmt.Println(a)}`,
		"untyped_numeric_parameters": `const scale=3
func half(x float64)float64{return x/2}
func total(xs ...float64)float64{n:=0.0;for _,x:=range xs{n+=x};return n/2}
func main(){fmt.Println(half(1),half(scale),half(1+2),half(float64(3)));f:=func(x float64)float64{return x/2};fmt.Println(f(3),total(1,2,3));defer fmt.Println(half(5));defer func(x float64){fmt.Println(x/2)}(3)}`,
		"typed_nil_containers": `func count(s []int,m map[string]int){fmt.Println(s==nil,m==nil,len(s),len(m));for range s{fmt.Println("bad")};for range m{fmt.Println("bad")};s=append(s,9)}
func main(){var s []int;var m map[string]int;count(s,m);count(nil,nil);fmt.Println(s==nil,m["missing"]);s=append(s,4);fmt.Println(s==nil,s[0]);s=nil;fmt.Println(s==nil,len(s));empty:=[]int{};fmt.Println(empty==nil);defer func(){fmt.Println(recover()!=nil)}();m["x"]=1}`,
		"nested_composite_literals": `type Point struct{X,Y float64}
func main(){a:=[][]float64{{0,0},{1,1}};fmt.Println(a[0][0],a[1][1]/2);m:=map[string][]int{"x":{1,2}};fmt.Println(m["x"][1]);ps:=[]Point{{X:1,Y:2},{3,4}};fmt.Println(ps[0].X/2,ps[1].Y/2);var zs []Point;zs=append(zs,Point{X:5});fmt.Println(zs[0].Y)}`,
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
