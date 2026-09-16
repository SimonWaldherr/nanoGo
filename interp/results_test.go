package interp

import (
	"context"
	"errors"
	"testing"
)

func TestResultsCompletionAndInputCopies(t *testing.T) {
	vm := NewInterpreter()
	if err := vm.BindInputs(map[string]any{"n": 7}); err != nil {
		t.Fatal(err)
	}
	src := `package main;func main(){n,err:=host.Input("n");if err!=nil{panic(err)};host.Emit("answer",n);panic("later")}`
	if err := vm.Run(src); err == nil {
		t.Fatal("expected panic")
	}
	r := vm.LastResults()
	if r.Committed || len(r.Events) != 1 || r.Events[0].Value != 7 {
		t.Fatalf("%+v", r)
	}
	if err := vm.Run(`package main;func main(){host.Emit("answer",8)}`); err != nil {
		t.Fatal(err)
	}
	r = vm.LastResults()
	if !r.Committed || len(r.Events) != 1 || r.Events[0].Value != 8 {
		t.Fatalf("%+v", r)
	}
}
func TestRegisteredHostFunctionContract(t *testing.T) {
	vm := NewInterpreter()
	if err := vm.RegisterHostFunction("double", HostFunctionSpec{Args: 1, Results: 2}, func(ctx context.Context, a []any) ([]any, error) { return []any{a[0].(int) * 2, nil}, ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(`package main;func main(){x,err:=double(3);if err!=nil||x!=6{panic("bad")}}`); err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(`package main;func main(){double()}`); !errors.Is(err, ErrHostContract) {
		t.Fatalf("%v", err)
	}
}

func TestStrictHostErrorsAndResultArity(t *testing.T) {
	failure := errors.New("host stopped")
	vm := NewInterpreter()
	_ = vm.RegisterHostFunction("fail", HostFunctionSpec{Results: 2}, func(context.Context, []any) ([]any, error) { return nil, failure })
	if err := vm.Run(`package main;func main(){_,err:=fail();_ = err}`); !errors.Is(err, failure) {
		t.Fatalf("swallowed callback: %v", err)
	}
	_ = vm.RegisterHostFunction("single", HostFunctionSpec{Results: 1}, func(context.Context, []any) ([]any, error) { return []any{3}, nil })
	if err := vm.Run(`package main;func main(){_,_ = single()}`); err == nil {
		t.Fatal("accepted two targets for one result")
	}
	vm.EnableResults()
	if err := vm.Run(`package main;func main(){_,_ = host.Emit("x",1)}`); err == nil {
		t.Fatal("accepted two targets for Emit")
	}
}
