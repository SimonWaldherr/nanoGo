package loader

import (
	"context"
	"simonwaldherr.de/go/nanogo/interp"
	"sync"
	"testing"
)

func TestPreparedFreshStateAndFailure(t *testing.T) {
	fs := interp.NewVFS()
	writeLoaderFile(t, fs, "/app/go.mod", "module example.com/app\n")
	writeLoaderFile(t, fs, "/app/main.go", `package main
var n=40
func init(){n++}
func main(){n++;x,_:=host.Input("fail");host.Emit("n",n);if x==true{panic("failed")}}`)
	p, err := PrepareModule(fs, "/app", Options{})
	if err != nil {
		t.Fatal(err)
	}
	writeLoaderFile(t, fs, "/app/main.go", `package main;func main(){panic("edited")}`)
	for _, fail := range []bool{true, false, false} {
		r, err := p.Run(context.Background(), RunOptions{Inputs: map[string]any{"fail": fail}})
		if (err != nil) != fail {
			t.Fatalf("error: %v", err)
		}
		if r.Results.Committed == fail || len(r.Results.Events) != 1 || r.Results.Events[0].Value != 42 {
			t.Fatalf("%+v", r)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := p.Run(context.Background(), RunOptions{})
			if e != nil || r.Results.Events[0].Value != 42 {
				t.Errorf("%+v %v", r, e)
			}
		}()
	}
	wg.Wait()
}
func TestPreparedUnifiedBudget(t *testing.T) {
	fs := interp.NewVFS()
	writeLoaderFile(t, fs, "/app/go.mod", "module example.com/app\n")
	writeLoaderFile(t, fs, "/app/main.go", `package main;func init(){for i:=0;i<10;i++{}};func main(){for i:=0;i<10;i++{}}`)
	p, e := PrepareModule(fs, "/app", Options{})
	if e != nil {
		t.Fatal(e)
	}
	r, e := p.Run(context.Background(), RunOptions{Configure: func(vm *interp.Interpreter) error { vm.Limits.MaxSteps = 100; return nil }})
	if e == nil || r.Diagnostic == nil || r.Diagnostic.Code != "limit.steps" {
		t.Fatalf("%+v %v", r, e)
	}
}
