package compat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
)

// The imported packages deliberately share both their package name and type
// names. Import aliases are lexical names, never runtime type identities.
func TestPackageTypeIdentity(t *testing.T) {
	files := map[string]string{
		"go.mod": "module identity.local/app\n\ngo 1.25\n",
		"one/types.go": `package shared
type Value struct{ N int }
type Alias = Value
type Code int
const Factor = 3
func (Value) Number()int{return 1}
func New()Value{return Value{N:7}}`,
		"two/types.go": `package shared
type Value struct{ N int }
type Code int
func (Value) Number()int{return 2}
func New()Value{return Value{N:8}}`,
		"main.go": `package main
import("fmt";a "identity.local/app/one";b "identity.local/app/two")
type Holder struct{ A a.Value; B b.Value }
func half(x float64)float64{return x/2}
func main(){x:=a.New();y:=b.New();fmt.Println(x.Number(),y.Number());var v any=x;_,aa:=v.(a.Value);_,ab:=v.(b.Value);_,alias:=v.(a.Alias);fmt.Println(aa,ab,alias);h:=Holder{A:a.Value{N:3},B:b.Value{N:4}};fmt.Println(h.A.Number(),h.B.Number());var zero a.Value;fmt.Println(zero.N,zero.Number());v=a.Code(3);_,ca:=v.(a.Code);_,cb:=v.(b.Code);_,ci:=v.(int);fmt.Println(ca,cb,ci);fmt.Println(half(a.Factor))}`,
	}
	dir := t.TempDir()
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)
	var out strings.Builder
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		out.WriteString(interp.ToString(args[0]))
		out.WriteByte('\n')
		return nil, nil
	})
	for name, source := range files {
		filename := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		filename = filepath.Join("/app", name)
		if err := vm.VFS.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := vm.VFS.WriteFile(filename, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	want, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Go reference: %v\n%s", err, want)
	}
	program, err := loader.LoadModule(vm.VFS, "/app", loader.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.RunProgram(context.Background(), vm, program, "main"); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Fatalf("Go: %q\nnanoGo: %q", want, out.String())
	}
}
