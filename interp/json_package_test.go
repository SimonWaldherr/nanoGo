package interp

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestJSONStandardSignaturesAndGuestErrors(t *testing.T) {
	out := runAndCapture(t, `package main
import("encoding/json";"fmt")
func main(){
 data,err:=json.Marshal(map[string]int{"answer":42});fmt.Println(string(data),err==nil)
 var dst map[string]int;err=json.Unmarshal(data,&dst);fmt.Println(dst["answer"],err==nil)
 err=json.Unmarshal([]byte("{"),&dst);fmt.Println(err!=nil,dst["answer"]);if len(err.Error())==0{panic("missing error message")};message:=err.Error;if message()!=err.Error(){panic("error method value")}
 data,err=json.Marshal(make(chan int));fmt.Println(data==nil,err!=nil)
 var n int;err=json.Unmarshal([]byte("1.5"),&n);fmt.Println(err!=nil,n)
 var target any;err=json.Unmarshal([]byte("[true,null,2]"),&target);data,err=json.Marshal(target);fmt.Println(string(data),err==nil)
}`)
	want := "{\"answer\":42} true\n42 true\ntrue 42\ntrue true\ntrue 0\n[true,null,2] true\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestJSONRejectsUnsupportedValuesAndCycles(t *testing.T) {
	vm := NewInterpreter()
	RegisterBuiltinPackages(vm)
	pkg, _ := vm.Package("encoding/json")
	cycle := &MapVal{KeyType: "string", ElementType: "any", Data: map[string]any{}}
	cycle.Data["self"] = cycle
	for _, value := range []any{cycle, math.NaN(), math.Inf(1), &Function{Name: "f"}, &MapVal{KeyType: "bool", ElementType: "int", Data: map[string]any{}}} {
		result, err := pkg.Funcs["Marshal"].Native([]any{value})
		if err != nil {
			t.Fatalf("ordinary marshal error escaped into interpreter: %v", err)
		}
		values, ok := result.(ReturnValues)
		if !ok || len(values) != 2 || values[1] == nil {
			t.Fatalf("marshal(%T) = %#v", value, result)
		}
	}
	var nested any = 1
	for i := 0; i < 140; i++ {
		nested = &SliceVal{ElementType: "any", Data: []any{nested}}
	}
	result, err := pkg.Funcs["Marshal"].Native([]any{nested})
	if err != nil || result.(ReturnValues)[1] == nil {
		t.Fatalf("deep nesting: %#v, %v", result, err)
	}
	_, err = pkg.Funcs["Marshal"].Native(nil)
	if err == nil || !strings.Contains(err.Error(), "argument") {
		t.Fatalf("wrong arity: %v", err)
	}
}

func TestJSONRejectsMalformedTypeMetadata(t *testing.T) {
	vm := NewInterpreter()
	vm.types["Duplicate"] = &TypeDef{Name: "Duplicate", Kind: "struct", Fields: []FieldDef{{Name: "X", Type: "int"}, {Name: "X", Type: "int"}}}
	if _, err := vm.marshalJSON(&StructVal{TypeName: "Duplicate", Fields: map[string]any{"X": 1}}); err == nil {
		t.Fatal("duplicate fields accepted")
	}
	if _, err := newJSONConversion(vm).typ("[1048576][1048576]int", 0); err == nil {
		t.Fatal("oversized native representation accepted")
	}
}

func TestJSONBudgetsAreExecutionFailures(t *testing.T) {
	for _, source := range []string{
		`package main;import "encoding/json";func main(){_,err:=json.Marshal("long payload");if err!=nil{panic("ordinary error")}}`,
		`package main;import "encoding/json";func main(){var result any;err:=json.Unmarshal(data,&result);if err!=nil{panic("ordinary error")}}`,
	} {
		vm := NewInterpreter()
		RegisterBuiltinPackages(vm)
		vm.declare("data", byteSliceValue([]byte(`[1,2,3,4,5]`)), vm.globals)
		vm.Limits.MaxAllocationUnits = 4
		if err := vm.Run(source); !errors.Is(err, ErrAllocationLimit) {
			t.Fatalf("expected allocation failure, got %v", err)
		}
		vm.Limits.MaxAllocationUnits = 0
		if err := vm.Run(`package main;func main(){}`); err != nil {
			t.Fatalf("run after failure: %v", err)
		}
	}
}

func TestJSONUnsupportedTypesAndPayloads(t *testing.T) {
	out := runAndCapture(t, `package main
import("encoding/json";"fmt")
type Child struct{N int}
type Embedded struct{Child}
type Recursive struct{Next *Recursive}
type Custom int
func(Custom) MarshalJSON()([]byte,error){return []byte("1"),nil}
func main(){
 _,err:=json.Marshal(Embedded{});fmt.Println(err!=nil)
 _,err=json.Marshal(Recursive{});fmt.Println(err!=nil)
 _,err=json.Marshal(Custom(1));fmt.Println(err!=nil)
 var p *int;err=json.Unmarshal([]byte("1"),p);fmt.Println(err!=nil)
 var n int8;err=json.Unmarshal([]byte("128"),&n);fmt.Println(n,err!=nil)
}`)
	if out != "true\ntrue\ntrue\ntrue\n0 true\n" {
		t.Fatalf("got %q", out)
	}
	vm := NewInterpreter()
	RegisterBuiltinPackages(vm)
	pkg, _ := vm.Package("encoding/json")
	result, err := pkg.Funcs["Unmarshal"].Native([]any{byteSliceValue([]byte(strings.Repeat("[", 140) + strings.Repeat("]", 140))), newPointerValue(nil, "any")})
	if err != nil || result.(ReturnValues)[0] == nil {
		t.Fatalf("deep payload: %v, %v", result, err)
	}
	result, err = pkg.Funcs["Unmarshal"].Native([]any{strings.Repeat("x", maxJSONBytes+1), newPointerValue(nil, "any")})
	if err != nil || result.(ReturnValues)[0] == nil {
		t.Fatalf("invalid byte input: %v, %v", result, err)
	}
}

func BenchmarkJSONRoundTrip(b *testing.B) {
	vm := NewInterpreter()
	value := &MapVal{KeyType: "string", ElementType: "[]float64", Data: map[string]any{
		"first":  &SliceVal{ElementType: "float64", Data: []any{1.25, 2.5, 3.75}},
		"second": &SliceVal{ElementType: "float64", Data: []any{4.25, 5.5, 6.75}},
	}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		data, err := vm.marshalJSON(value)
		if err != nil {
			b.Fatal(err)
		}
		target := newPointerValue(nil, "any")
		if err := vm.unmarshalJSON(data, target); err != nil {
			b.Fatal(err)
		}
	}
}

func TestJSONLegacyImport(t *testing.T) {
	out := runAndCapture(t, `package main
import(json "nanogo/jsonlegacy";"fmt")
func main(){text:=json.Marshal(map[string]int{"n":3});v:=json.Unmarshal(text);fmt.Println(text,v!=nil)}`)
	if out != "{\"n\":3} true\n" {
		t.Fatalf("got %q", out)
	}
}

func TestJSONResultArity(t *testing.T) {
	for _, statement := range []string{
		`a,b:=json.Unmarshal([]byte("null"),&value);_,_=a,b`,
		`var a,b=json.Unmarshal([]byte("null"),&value);_,_=a,b`,
		`a,b:=json.Unmarshal([]byte("{"),&value);_,_=a,b`,
		`data:=json.Marshal(value);_=data`,
		`var data=json.Marshal(value);_=data`,
	} {
		vm := NewInterpreter()
		RegisterBuiltinPackages(vm)
		err := vm.Run(`package main;import "encoding/json";func main(){var value any;` + statement + `}`)
		if err == nil || !strings.Contains(err.Error(), "assignment count mismatch") {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestJSONSliceAliases(t *testing.T) {
	out := runAndCapture(t, `package main
import("encoding/json";"fmt")
func main(){
 values:=[]int{0};alias:=values;err:=json.Unmarshal([]byte("[2,3]"),&values);fmt.Println(err==nil,alias[0],values[1])
 err=json.Unmarshal([]byte("[]"),&values);fmt.Println(err==nil,len(values),cap(values),values==nil)
 bytes:=[]byte{0};oldBytes:=bytes;err=json.Unmarshal([]byte("\"AQI=\""),&bytes);fmt.Println(err==nil,oldBytes[0],bytes[1])
 err=json.Unmarshal([]byte("[4,5]"),&bytes);fmt.Println(err==nil,bytes[0],bytes[1])
}`)
	if out != "true 2 3\ntrue 0 0 false\ntrue 0 2\ntrue 4 5\n" {
		t.Fatalf("got %q", out)
	}
}
