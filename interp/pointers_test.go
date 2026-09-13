package interp

import "testing"

func TestPointerLocationsAndLifetime(t *testing.T) {
	for _, traced := range []bool{false, true} {
		vm, out := newTestVM()
		if traced {
			vm.SetTracer(NewTracer(32))
		}
		err := vm.Run(`package main
import "fmt"
func keep(n int)*int{return &n}
func main(){
 a:=1;p:=&a;*p=4;fmt.Println(a,*p,p==&a)
 q:=keep(7);r:=keep(8);fmt.Println(*q,*r,q==r)
 {var x=9;q=&x};{var x=10;r=&x};fmt.Println(*q,*r)
 nums:=[]int{1,2};p=&nums[0];*p=5;fmt.Println(nums[0],p==&nums[0])
 ps:=[]*int{};for i:=range 3{ps=append(ps,&i)};fmt.Println(*ps[0],*ps[1],*ps[2])
 n:=new(int);fmt.Println(*n);*n=3;fmt.Println(*n)
}`)
		if err != nil {
			t.Fatal(err)
		}
		want := "4 4 true\n7 8 false\n9 10\n5 true\n0 1 2\n0\n3\n"
		if out.String() != want {
			t.Fatalf("traced=%v got %q", traced, out.String())
		}
	}
}

func TestPointerToChannel(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func replace(p *chan int){*p=make(chan int,2);*p<-7;*p<-8;close(*p)}
func main(){var ch chan int;replace(&ch);for n:=range ch{fmt.Println(n)};p:=new(chan int);*p=make(chan int,1);*p<-9;fmt.Println(<-*p)}`)
	if out != "7\n8\n9\n" {
		t.Fatalf("got %q", out)
	}
}

func TestPointerStructAndNilRecovery(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
type Item struct {Value int}
func (i *Item) Inc(){i.Value++}
func main(){p:=&Item{Value:3};p.Inc();p.Value++;fmt.Println(p.Value,(*p).Value);defer func(){fmt.Println(recover()!=nil)}();var n *int;fmt.Println(*n)}`)
	if out != "5 5\ntrue\n" {
		t.Fatalf("got %q", out)
	}
}

func TestNilPointerAndChannel(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func main(){var p *int;var ch chan int;fmt.Println(p==nil,ch==nil,*new(chan int)==nil);select{case <-ch:fmt.Println("bad");default:fmt.Println("default")}}`)
	if out != "true true true\ndefault\n" {
		t.Fatalf("got %q", out)
	}
}

func TestPointerReflectionAndInterfaces(t *testing.T) {
	out := runAndCapture(t, `package main
import("fmt";"reflect";"encoding/json")
type Item struct{Value int}
func(p *Item) Get()int{return p.Value}
type Getter interface{Get()int}
func main(){p:=&Item{Value:7};var x any=p;g,ok:=x.(Getter);fmt.Println(ok,g.Get());fmt.Println(reflect.TypeOf(p).Kind()==reflect.Ptr,reflect.TypeOf(p).Elem().Name());fmt.Println(reflect.ValueOf(p).Elem().Field(0).Int());var n *Item;fmt.Println(reflect.ValueOf(n).IsNil(),reflect.ValueOf(n).Elem().IsValid());fmt.Println(reflect.DeepEqual(p,&Item{Value:7}));s,err:=json.Marshal(p);fmt.Println(s,err==nil)}`)
	if out != "true 7\ntrue Item\n7\ntrue false\ntrue\n{\"Value\":7} true\n" {
		t.Fatalf("got %q", out)
	}
}

func TestPointerIdentityAfterMutation(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func keep(n int)*int{return &n}
func main(){p:=keep(7);q:=&*p;fmt.Println(p==q);values:=[]int{1,2};m:=map[*int]string{};m[&values[0]]="kept";values[0]=8;fmt.Println(m[&values[0]]);n:=map[*int]int{};n[p]=3;*p=9;fmt.Println(n[q])}`)
	if out != "true\nkept\n3\n" {
		t.Fatalf("got %q", out)
	}
}

func BenchmarkPointerReadWrite(b *testing.B) {
	vm, _ := newTestVM()
	const src = `package main
func main(){n:=0;p:=&n;for i:=0;i<1000;i++{*p=*p+1};if n!=1000{panic("bad pointer")}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatal(err)
		}
	}
}
