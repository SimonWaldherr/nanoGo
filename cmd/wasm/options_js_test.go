package main

import (
	"encoding/json"
	"simonwaldherr.de/go/nanogo/interp"
	"syscall/js"
	"testing"
)

func TestStructuredRunOptions(t *testing.T) {
	args := []js.Value{js.ValueOf(`package main;func main(){v,err:=host.Input("n");if err!=nil{panic(err)};host.Emit("answer",v)}`), js.ValueOf(false), js.ValueOf(false), js.Null(), js.ValueOf(`{"inputs":{"n":7},"limits":{"maxSteps":100}}`)}
	var result runStats
	if err := json.Unmarshal([]byte(jsNanoGoRun(js.Undefined(), args).(string)), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || !result.ResultsCommitted || len(result.Results) != 1 || result.Results[0].Value != float64(7) {
		t.Fatalf("%+v", result)
	}
	args[0] = js.ValueOf(`package main;func main(){host.Emit("partial",1);panic("oops")}`)
	if err := json.Unmarshal([]byte(jsNanoGoRun(js.Undefined(), args).(string)), &result); err != nil {
		t.Fatal(err)
	}
	if result.ResultsCommitted || result.Diagnostic == nil || result.Diagnostic.Code != "runtime.panic" {
		t.Fatalf("%+v", result)
	}
}
func TestRunOptionsValidation(t *testing.T) {
	for _, raw := range []string{`{"limits":{"maxSteps":-1}}`, `{"limits":{"maxGoroutines":-1}}`, `{"limits":{"unknown":1}}`, `{"inputs":[]}`, `{"unknown":1}`} {
		vm := interp.NewInterpreter()
		args := []js.Value{js.Null(), js.Null(), js.Null(), js.Null(), js.ValueOf(raw)}
		if err := configureExecution(vm, args); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
