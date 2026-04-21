// cmd/wasm/main.go
package main

import (
	"encoding/json"
	"syscall/js"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/runtime"
)

// activeCanvas holds the currently bound canvas from the host page.
var activeCanvas runtime.CanvasBinding

// jsNanoGoRun runs nanoGo on a source string coming from JS.
func jsNanoGoRun(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		runtime.ConsoleError("nanoGoRun: missing source")
		return nil
	}
	source := args[0].String()

	vm := interp.NewInterpreter()

	// Register stdlib-like host natives and built-in packages (fmt, time, math, json, sync, regexp, strings, sort, math/rand, browser, text/template, http, storage).
	runtime.RegisterHostNatives(vm, &activeCanvas)
	interp.RegisterBuiltinPackages(vm)

	if err := vm.Run(source); err != nil {
		runtime.ConsoleError("nanoGo error: " + err.Error())
	}
	return nil
}

// jsNanoGoFormat formats Go source code using gofmt rules.
// Returns a JS object: { source: "..." } on success, { error: "..." } on failure.
func jsNanoGoFormat(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return js.Global().Get("Object").New()
	}
	src := args[0].String()
	formatted, err := interp.FormatSource(src)
	result := js.Global().Get("Object").New()
	if err != nil {
		result.Set("error", err.Error())
	} else {
		result.Set("source", formatted)
	}
	return result
}

// jsNanoGoVet runs basic static analysis on Go source code.
// Returns a JS array of { line, column, message } objects, or { error: "..." } on parse failure.
func jsNanoGoVet(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return js.Global().Get("Array").New(0)
	}
	src := args[0].String()
	issues, err := interp.VetSource(src)
	if err != nil {
		errObj := js.Global().Get("Object").New()
		errObj.Set("error", err.Error())
		return errObj
	}
	arr := js.Global().Get("Array").New(len(issues))
	for i, iss := range issues {
		obj := js.Global().Get("Object").New()
		obj.Set("line", iss.Line)
		obj.Set("column", iss.Column)
		obj.Set("message", iss.Message)
		arr.SetIndex(i, obj)
	}
	return arr
}

// jsNanoGoVersion returns a JSON object with version/capability information
// so the playground UI can detect which features are available.
func jsNanoGoVersion(this js.Value, args []js.Value) any {
	info := map[string]any{
		"version":   "0.1.0",
		"hasFormat": true,
		"hasVet":    true,
		"hasOS":     true,
	}
	b, _ := json.Marshal(info)
	return string(b)
}

// jsNanoGoSetCanvas binds a canvas by element id and optional cell scale.
func jsNanoGoSetCanvas(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		runtime.ConsoleError("nanoGoSetCanvas: missing elementId")
		return nil
	}
	elementId := args[0].String()
	scale := 10
	if len(args) >= 2 {
		scale = args[1].Int()
	}
	activeCanvas = runtime.BindCanvasById(elementId, scale)
	return nil
}

// jsNanoGoSetScale adjusts the logical cell size for pixel-art rendering.
func jsNanoGoSetScale(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return nil
	}
	scale := args[0].Int()
	activeCanvas.CellSize = scale
	return nil
}

func main() {
	js.Global().Set("nanoGoRun", js.FuncOf(jsNanoGoRun))
	js.Global().Set("nanoGoFormat", js.FuncOf(jsNanoGoFormat))
	js.Global().Set("nanoGoVet", js.FuncOf(jsNanoGoVet))
	js.Global().Set("nanoGoVersion", js.FuncOf(jsNanoGoVersion))
	js.Global().Set("nanoGoSetCanvas", js.FuncOf(jsNanoGoSetCanvas))
	js.Global().Set("nanoGoSetScale", js.FuncOf(jsNanoGoSetScale))

	// Block forever for the browser event loop.
	select {}
}
