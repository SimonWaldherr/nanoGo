// Run with: go run ./examples/prepared
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
)

func main() {
	program, err := loader.PrepareSource(`package main
import "nanogo/host"
var runs int
func main(){
 runs++
 input,err:=host.Input("value");if err!=nil{panic(err)}
 if err:=host.Emit("answer",map[string]any{"value":input,"runs":runs});err!=nil{panic(err)}
}`)
	if err != nil {
		panic(err)
	}
	for _, value := range []int{7, 9} {
		result, err := program.Run(context.Background(), loader.RunOptions{Inputs: map[string]any{"value": value}, Configure: func(vm *interp.Interpreter) error {
			vm.Limits.MaxCallDepth = 64
			vm.Limits.MaxAllocationUnits = 10000
			vm.Limits.MaxResultBytes = 4096
			return nil
		}})
		if err != nil {
			fmt.Fprintln(os.Stderr, result.Diagnostic)
			os.Exit(1)
		}
		if !result.Results.Committed {
			panic("incomplete results")
		}
		data, err := json.Marshal(result.Results.Events)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(data))
	}
}
