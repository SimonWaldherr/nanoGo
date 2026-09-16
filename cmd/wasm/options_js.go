//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"simonwaldherr.de/go/nanogo/interp"
	"strings"
	"syscall/js"
)

// configureExecution is the optional fifth protocol argument. Limits omitted
// from this object retain host defaults; explicit zero disables that limit.
func configureExecution(vm *interp.Interpreter, args []js.Value) error {
	inputs := map[string]any{}
	if len(args) > 4 && !args[4].IsUndefined() && !args[4].IsNull() {
		if args[4].Type() != js.TypeString {
			return fmt.Errorf("run options must be JSON string")
		}
		raw := args[4].String()
		if len(raw) > 16<<20 {
			return fmt.Errorf("run options exceed 16MiB")
		}
		var opts struct {
			Inputs map[string]any   `json:"inputs"`
			Limits *json.RawMessage `json:"limits"`
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&opts); err != nil {
			return fmt.Errorf("run options: %w", err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return fmt.Errorf("run options: trailing JSON data")
		}
		if opts.Inputs != nil {
			inputs = opts.Inputs
		}
		if opts.Limits != nil {
			decoder = json.NewDecoder(strings.NewReader(string(*opts.Limits)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&vm.Limits); err != nil {
				return fmt.Errorf("execution limits: %w", err)
			}
			if vm.Limits.MaxGoroutines < 0 {
				return fmt.Errorf("maxGoroutines must be nonnegative")
			}
		}
	}
	return vm.BindInputs(inputs)
}
