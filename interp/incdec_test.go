package interp

import (
	"sync"
	"testing"
)

func TestIncDecBindingsAndLvalues(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
type Counter struct{ value int }
func main(){
 a,b,c,d,e:=10,20,30,40,50
 a++;b--;c++;d--;e++
 { a:=100; a--;fmt.Println(a) }
 fmt.Println(a,b,c,d,e)
 calls:=0
 index:=func() int {calls++;return 0}
 s:=[]int{5};s[index()]++;s[index()]--
 m:=map[string]int{};m["n"]++;m["n"]--
 p:=Counter{value:7};p.value++;p.value--
 fmt.Println(s[0],m["n"],p.value,calls)
}`)
	if out != "99\n11 19 31 39 51\n5 0 7 2\n" {
		t.Fatalf("output=%q", out)
	}
}

func TestAddIntSharedScopes(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		vm := NewInterpreter()
		env := NewEnv(nil)
		env.shared = !concurrent
		for _, name := range []string{"a", "b", "c", "d"} {
			vm.declareInt(name, 0, env)
		}
		if concurrent {
			exec := &execution{}
			exec.concurrent.Store(true)
			vm.activeExecution = exec
		}
		child := NewEnv(env)
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 1000; i++ {
					vm.addInt("a", 1, child)
					vm.addInt("d", -1, child)
				}
			}()
		}
		wg.Wait()
		for name, want := range map[string]int{"a": 8000, "d": -8000} {
			if got, _ := vm.get(name, env); got != want {
				t.Fatalf("%s=%v want %d", name, got, want)
			}
		}
	}
}
