package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRecycledScopesDoNotLeakBetweenInterpreters(t *testing.T) {
	const source = `package main
func fill(){
 a,b,c,d,e:=1,2,3,4,5
 x,y,z,w:=1.5,2.5,3.5,4.5
 s,t,u,v:="a","b","c","d"
 stop()
}
func main(){fill()}`
	for _, failure := range []error{nil, context.Canceled, errors.New("host failure")} {
		for _, name := range []string{"a", "b", "e", "x", "w", "s", "v"} {
			vm := NewInterpreter()
			vm.RegisterNativeContext("stop", func(context.Context, []any) (any, error) { return nil, failure })
			if err := vm.Run(source); !errors.Is(err, failure) {
				t.Fatalf("fill: %v", err)
			}
			fresh := NewInterpreter()
			err := fresh.Run("package main;func main(){_=" + name + "}")
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("local %s leaked after %v: %v", name, failure, err)
			}
		}
	}
}

func TestRecycledScopesConcurrentInterpreters(t *testing.T) {
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				vm, out := newTestVM()
				value := worker*100 + i
				src := fmt.Sprintf(`package main
var base=%d
func f(n int)int{a:=n+base;if n>0{return a+f(n-1)};return a}
func main(){ConsoleLog(f(3))}`, value)
				if err := vm.Run(src); err != nil {
					t.Error(err)
					return
				}
				if got := strings.TrimSpace(out.String()); got != fmt.Sprint(4*value+6) {
					t.Errorf("interpreter %d: %s", value, got)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}
