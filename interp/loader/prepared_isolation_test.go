package loader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestPreparedDependencyStateInputsAndInitFailure(t *testing.T) {
	fs := interp.NewVFS()
	writeLoaderFile(t, fs, "/app/go.mod", "module prepared.local/app\n")
	writeLoaderFile(t, fs, "/lib/go.mod", "module prepared.local/library\n")
	writeLoaderFile(t, fs, "/lib/state/state.go", `package state
var count=10
var values=[][]int{{1,2}}
func init(){count++;values[0][0]++}
func Change(delta int)int{count+=delta;values[0][0]+=delta;return count+values[0][0]}
func Snapshot()int{return count+values[0][0]}`)
	writeLoaderFile(t, fs, "/app/main.go", `package main
import "prepared.local/library/state"
func init(){host.Emit("init",state.Snapshot());fail,_:=host.Input("failInit");if fail==true{state.Change(99);panic("init failed")}}
func main(){value,_:=host.Input("delta");host.Emit("changed",state.Change(value.(int)));fail,_:=host.Input("failMain");if fail==true{panic("main failed")}}`)
	opts := Options{DependencyRoots: map[string]string{"prepared.local/library": "/lib"}}
	p, err := PrepareModule(fs, "/app", opts)
	if err != nil {
		t.Fatal(err)
	}
	// Both source mutations and caller-owned configuration mutations after
	// preparation must leave the prepared dependency snapshot unchanged.
	opts.DependencyRoots["prepared.local/library"] = "/missing"
	writeLoaderFile(t, fs, "/lib/state/state.go", "package state;func init(){panic(\"edited\")}")
	for _, stage := range []string{"init", "main", "success", "success"} {
		r, err := p.Run(context.Background(), RunOptions{Inputs: map[string]any{"delta": 3, "failInit": stage == "init", "failMain": stage == "main"}})
		if (err != nil) != (stage != "success") {
			t.Fatalf("%s error = %v", stage, err)
		}
		if r.Results.Committed != (stage == "success") {
			t.Fatalf("%s committed = %v", stage, r.Results.Committed)
		}
		wantEvents := 2
		if stage == "init" {
			wantEvents = 1
		}
		if len(r.Results.Events) != wantEvents || r.Results.Events[0].Value != 13 {
			t.Fatalf("%s events: %+v", stage, r.Results.Events)
		}
		if wantEvents == 2 && r.Results.Events[1].Value != 19 {
			t.Fatalf("%s leaked state: %+v", stage, r.Results.Events)
		}
	}
	var wg sync.WaitGroup
	for delta := 0; delta < 8; delta++ {
		wg.Add(1)
		go func(delta int) {
			defer wg.Done()
			r, err := p.Run(context.Background(), RunOptions{Inputs: map[string]any{"delta": delta}})
			if err != nil || !r.Results.Committed || len(r.Results.Events) != 2 {
				t.Errorf("concurrent delta %d: %+v, %v", delta, r, err)
				return
			}
			if r.Results.Events[0].Value != 13 || r.Results.Events[1].Value != 13+2*delta {
				t.Errorf("delta %d leaked: %+v", delta, r.Results.Events)
			}
		}(delta)
	}
	wg.Wait()
}

func TestPreparedCancellationAndFreshVFSSnapshot(t *testing.T) {
	fs := interp.NewVFS()
	writeLoaderFile(t, fs, "/app/go.mod", "module prepared.local/app\n")
	writeLoaderFile(t, fs, "/app/main.go", `package main;func main(){touch();wait()}`)
	writeLoaderFile(t, fs, "/state.txt", "original")
	p, err := PrepareModule(fs, "/app", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelRun := range []bool{true, false, false} {
		ctx, cancel := context.WithCancel(context.Background())
		options := RunOptions{Configure: func(vm *interp.Interpreter) error {
			data, err := vm.VFS.ReadFile("/state.txt")
			if err != nil {
				return err
			}
			if string(data) != "original" {
				return fmt.Errorf("leaked VFS: %q", data)
			}
			vm.RegisterNative("touch", func([]any) (any, error) { return nil, vm.VFS.WriteFile("/state.txt", []byte("changed"), 0600) })
			vm.RegisterNativeContext("wait", func(ctx context.Context, _ []any) (any, error) {
				if cancelRun {
					cancel()
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, nil
			})
			return nil
		}}
		r, err := p.Run(ctx, options)
		cancel()
		if cancelRun {
			if !errors.Is(err, context.Canceled) || r.Results.Committed {
				t.Fatalf("cancellation: %+v %v", r, err)
			}
		} else if err != nil || !r.Results.Committed {
			t.Fatalf("fresh success: %+v %v", r, err)
		}
	}
}

func TestPreparedDependencyDiagnosticPosition(t *testing.T) {
	fs := interp.NewVFS()
	writeLoaderFile(t, fs, "/app/go.mod", "module prepared.local/app\n")
	writeLoaderFile(t, fs, "/app/main.go", "package main\nimport \"prepared.local/app/helper\"\nfunc main(){helper.Fail()}\n")
	writeLoaderFile(t, fs, "/app/helper/fail.go", "package helper\n\nfunc Fail() {\n    panic(\"dependency failure\")\n}\n")
	p, err := PrepareModule(fs, "/app", Options{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), RunOptions{})
	if err == nil || r.Diagnostic == nil {
		t.Fatalf("missing diagnostic: %+v %v", r, err)
	}
	loc := r.Diagnostic.Location
	if loc == nil || loc.File != "/app/helper/fail.go" || loc.Line != 4 || loc.Column != 5 {
		t.Fatalf("dependency position: %+v", loc)
	}
	if len(r.Diagnostic.Stack) < 2 || r.Diagnostic.Stack[0].Function != "Fail" || r.Diagnostic.Stack[0].Location.File != "/app/helper/fail.go" {
		t.Fatalf("dependency stack: %+v", r.Diagnostic.Stack)
	}
}
