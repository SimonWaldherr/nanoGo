// cmd/wasm/main.go
package main

import (
	"syscall/js"

	"nanogo/interp"
	"nanogo/runtime"
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
	js.Global().Set("nanoGoSetCanvas", js.FuncOf(jsNanoGoSetCanvas))
	js.Global().Set("nanoGoSetScale", js.FuncOf(jsNanoGoSetScale))

	// Block forever for the browser event loop.
	select {}
}
