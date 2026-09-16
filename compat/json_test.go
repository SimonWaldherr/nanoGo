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

func TestJSONStandardGo(t *testing.T) {
	const source = `package main
import("fmt";"encoding/json")
type Child struct { Value int ` + "`json:\"value\"`" + ` }
type Count int
type Bytes []byte
type Record struct {
 Name string ` + "`json:\"name\"`" + `
 Children []Child ` + "`json:\"children\"`" + `
 Bytes []byte ` + "`json:\"bytes\"`" + `
 Empty string ` + "`json:\"empty,omitempty\"`" + `
 Hidden int ` + "`json:\"-\"`" + `
}
func main(){
 var r Record
 err:=json.Unmarshal([]byte("{\"NAME\":\"nanoGo\",\"children\":[{\"value\":7}],\"bytes\":\"AAH/\",\"ignored\":true}"),&r)
 fmt.Println(err==nil,r.Name,r.Children[0].Value,len(r.Bytes),r.Bytes[2])
 data,err:=json.Marshal(r);fmt.Println(string(data),err==nil)
 var m map[string][]int;err=json.Unmarshal([]byte("{\"xs\":[1,2],\"none\":null}"),&m)
 fmt.Println(err==nil,len(m["xs"]),m["none"]==nil)
 data,err=json.Marshal(m);fmt.Println(string(data),err==nil)
 var xs []int;data,err=json.Marshal(xs);fmt.Println(string(data),err==nil)
 xs=[]int{};data,err=json.Marshal(xs);fmt.Println(string(data),err==nil)
 n:=9;err=json.Unmarshal([]byte("null"),&n);fmt.Println(n,err==nil)
 err=json.Unmarshal([]byte("false"),&n);fmt.Println(n,err!=nil)
 err=json.Unmarshal([]byte("{"),&r);fmt.Println(err!=nil,r.Name)
 p:=&Child{Value:3};alias:=p;err=json.Unmarshal([]byte("{\"value\":8}"),&p);fmt.Println(err==nil,alias.Value,p==alias)
 var anyValue any;err=json.Unmarshal([]byte("{\"x\":[true,null,1.25]}"),&anyValue);data,err=json.Marshal(anyValue);fmt.Println(string(data),err==nil)
 pointers:=map[string]*Child{"x":p};err=json.Unmarshal([]byte("{\"x\":{\"value\":11}}"),&pointers);fmt.Println(err==nil,pointers["x"].Value,p.Value,pointers["x"]==p)
 var intoPointer any=p;err=json.Unmarshal([]byte("{\"value\":12}"),&intoPointer);fmt.Println(err==nil,p.Value)
 var count Count;err=json.Unmarshal([]byte("5"),&count);data,err=json.Marshal(count);fmt.Println(string(data),err==nil);var boxed any=count;_,ok:=boxed.(Count);fmt.Println(ok)
 numbers:=map[int]string{2:"two",1:"one"};data,err=json.Marshal(numbers);fmt.Println(string(data),err==nil)
 fixed:=[3]byte{0,1,2};data,err=json.Marshal(fixed);fmt.Println(string(data),err==nil)
 growing:=[]int{0};shared:=growing;err=json.Unmarshal([]byte("[2,3]"),&growing);fmt.Println(err==nil,shared[0],growing[0],growing[1])
 err=json.Unmarshal([]byte("[]"),&growing);fmt.Println(err==nil,len(growing),cap(growing),growing==nil)
 var scalar any=7;err=json.Unmarshal([]byte("null"),&scalar);fmt.Println(err==nil,scalar==nil)
 original:=map[string]int{"old":1};aliasMap:=original;err=json.Unmarshal([]byte("{\"new\":2}"),&original);fmt.Println(err==nil,aliasMap["new"],original["old"])
 err=json.Unmarshal([]byte("null"),&original);fmt.Println(err==nil,original==nil,aliasMap["old"])
 bytes:=[]byte{0};aliasBytes:=bytes;err=json.Unmarshal([]byte("\"AQI=\""),&bytes);fmt.Println(err==nil,aliasBytes[0],bytes[0],bytes[1])
 var fromNamed int;err=json.Unmarshal(Bytes([]byte("7")),&fromNamed);fmt.Println(err==nil,fromNamed)
 backing:=[]int{10,20,30,40};reuse:=backing[:2]
 err=json.Unmarshal([]byte("[1,2,3]"),&reuse);fmt.Println(err==nil,len(reuse),backing[0],backing[2],backing[3])
 err=json.Unmarshal([]byte("[9,\"bad\",7]"),&reuse);fmt.Println(err!=nil,backing[0],backing[1],backing[2],backing[3])
}`
	file := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	want, err := exec.CommandContext(ctx, "go", "run", file).CombinedOutput()
	if err != nil {
		t.Fatalf("Go: %v\n%s", err, want)
	}
	for _, loaded := range []bool{false, true} {
		vm := interp.NewInterpreter()
		interp.RegisterBuiltinPackages(vm)
		var out strings.Builder
		vm.RegisterNative("ConsoleLog", func(args []any) (any, error) { out.WriteString(interp.ToString(args[0]) + "\n"); return nil, nil })
		if loaded {
			if err := vm.VFS.MkdirAll("/app", 0755); err != nil {
				t.Fatal(err)
			}
			if err := vm.VFS.WriteFile("/app/main.go", []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			p, err := loader.LoadModule(vm.VFS, "/app", loader.Options{ModulePath: "compat.local/app"})
			if err != nil {
				t.Fatal(err)
			}
			err = loader.RunProgram(ctx, vm, p, "main")
			if err != nil {
				t.Fatal(err)
			}
		} else if err := vm.RunContext(ctx, source); err != nil {
			t.Fatal(err)
		}
		if out.String() != string(want) {
			t.Fatalf("loaded=%v\nGo: %q\nnanoGo: %q", loaded, want, out.String())
		}
	}
}
