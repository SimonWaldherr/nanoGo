// cmd/wasm/main.go
package main

import (
	"encoding/json"
	"syscall/js"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/runtime"
)

// activeCanvas holds the currently bound canvas from the host page.
var activeCanvas runtime.CanvasBinding

// newPlaygroundVM builds an interpreter configured exactly like the
// playground expects: private in-memory VFS, HTTP allowed (still subject to
// browser CORS), and all curated packages registered.
func newPlaygroundVM() *interp.Interpreter {
	vm := interp.NewInterpreter()
	// Browser requests remain subject to same-origin/CORS rules. The guest gets
	// a private, in-memory VFS only — never the browser host's filesystem — so
	// read/write access is safe and lets the Virtual FS demo work as advertised.
	vm.Capabilities.FileSystem = interp.FileSystemCapabilities{Read: true, Write: true}
	vm.Capabilities.Network.HTTP = true

	// Register host natives and nanoGo's curated package subsets (including
	// fmt, time, math, json, sync, regexp, strings, sort, strconv, path,
	// unicode/utf8, math/rand, browser, text/template, http, and storage).
	runtime.RegisterHostNatives(vm, &activeCanvas)
	interp.RegisterBuiltinPackages(vm)
	return vm
}

// traceEventJSON is the wire form of one interp.TraceEvent. Times are
// milliseconds relative to the first retained event so the UI can render a
// timeline without caring about absolute clocks.
type traceEventJSON struct {
	Seq  uint64  `json:"seq"`
	Ms   float64 `json:"ms"`
	Kind string  `json:"kind"`
	Fn   string  `json:"fn,omitempty"`
	Msg  string  `json:"msg,omitempty"`
	Line int     `json:"line,omitempty"`
	Col  int     `json:"col,omitempty"`
}

// runStats is what jsNanoGoRun returns (as a JSON string) so the host can
// show execution details: interpreter wall time, deterministic step count,
// and an optional bounded trace timeline.
type runStats struct {
	ElapsedMs float64          `json:"elapsedMs"`
	Steps     uint64           `json:"steps"`
	Error     string           `json:"error,omitempty"`
	Trace     []traceEventJSON `json:"trace,omitempty"`
	TraceCap  int              `json:"traceCap,omitempty"`
}

// jsNanoGoRun runs nanoGo on a source string coming from JS. An optional
// truthy second argument enables local tracing; the result is a JSON string
// with elapsedMs/steps/error/trace so hosts can surface run details.
func jsNanoGoRun(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		runtime.ConsoleError("nanoGoRun: missing source")
		return nil
	}
	source := args[0].String()
	wantTrace := len(args) >= 2 && args[1].Truthy()

	vm := newPlaygroundVM()

	const traceCapacity = 4096
	var tracer *interp.Tracer
	if wantTrace {
		tracer = interp.NewTracer(traceCapacity)
		vm.SetTracer(tracer)
	}

	stats := runStats{}
	start := time.Now()
	err := vm.Run(source)
	stats.ElapsedMs = float64(time.Since(start).Microseconds()) / 1000
	stats.Steps = vm.LastStepCount()
	if err != nil {
		stats.Error = err.Error()
		runtime.ConsoleError("nanoGo error: " + err.Error())
	}

	if tracer != nil {
		events := tracer.Events()
		stats.TraceCap = traceCapacity
		stats.Trace = make([]traceEventJSON, 0, len(events))
		var epoch time.Time
		for i, ev := range events {
			if i == 0 {
				epoch = ev.At
			}
			stats.Trace = append(stats.Trace, traceEventJSON{
				Seq:  ev.Sequence,
				Ms:   float64(ev.At.Sub(epoch).Microseconds()) / 1000,
				Kind: ev.Kind,
				Fn:   ev.Function,
				Msg:  ev.Message,
				Line: ev.Location.Line,
				Col:  ev.Location.Column,
			})
		}
	}

	b, jsonErr := json.Marshal(stats)
	if jsonErr != nil {
		return nil
	}
	return string(b)
}

// jsNanoGoAst parses source (without executing it) and returns a JSON string
// with the syntax tree and structural stats: {tree, nodeCount, maxDepth,
// funcs, imports, parseUs} or {error} on parse failure.
func jsNanoGoAst(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return `{"error":"missing source"}`
	}
	res, err := interp.InspectSource(args[0].String())
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	b, jsonErr := json.Marshal(res)
	if jsonErr != nil {
		b, _ = json.Marshal(map[string]string{"error": jsonErr.Error()})
	}
	return string(b)
}

// jsNanoGoCallGraph parses source (without executing it) and returns a JSON
// string with the per-function call graph: {funcs:[{name, recv, line, calls,
// calledBy}]} or {error} on parse failure. See interp.AnalyzeCallGraph for
// the resolution rules.
func jsNanoGoCallGraph(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return `{"error":"missing source"}`
	}
	res, err := interp.AnalyzeCallGraph(args[0].String())
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	b, jsonErr := json.Marshal(res)
	if jsonErr != nil {
		b, _ = json.Marshal(map[string]string{"error": jsonErr.Error()})
	}
	return string(b)
}

// benchStats reports one nanoGoBench call. Steps are per run and — being
// evaluator checkpoints, not CPU time — identical across machines for the
// same program. Wall times are informational.
type benchStats struct {
	Iterations int       `json:"iterations"`
	StepsPerOp uint64    `json:"stepsPerOp"`
	AvgMs      float64   `json:"avgMs"`
	MinMs      float64   `json:"minMs"`
	MaxMs      float64   `json:"maxMs"`
	RunsMs     []float64 `json:"runsMs"`
	Error      string    `json:"error,omitempty"`
}

// jsNanoGoBench runs the whole program N times, each on a fresh interpreter,
// and returns aggregate timing plus the deterministic steps-per-run metric as
// a JSON string. The host is expected to silence guest output while this
// runs (the playground worker does).
func jsNanoGoBench(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return `{"error":"missing source"}`
	}
	source := args[0].String()
	n := 5
	if len(args) >= 2 {
		if v := args[1].Int(); v > 0 {
			n = v
		}
	}
	if n > 100 {
		n = 100
	}

	stats := benchStats{Iterations: n, RunsMs: make([]float64, 0, n)}
	var totalMs float64
	for i := 0; i < n; i++ {
		vm := newPlaygroundVM()
		start := time.Now()
		err := vm.Run(source)
		elapsedMs := float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			stats.Error = err.Error()
			break
		}
		stats.StepsPerOp = vm.LastStepCount()
		stats.RunsMs = append(stats.RunsMs, elapsedMs)
		totalMs += elapsedMs
		if i == 0 || elapsedMs < stats.MinMs {
			stats.MinMs = elapsedMs
		}
		if elapsedMs > stats.MaxMs {
			stats.MaxMs = elapsedMs
		}
	}
	if len(stats.RunsMs) > 0 {
		stats.AvgMs = totalMs / float64(len(stats.RunsMs))
	}

	b, jsonErr := json.Marshal(stats)
	if jsonErr != nil {
		b, _ = json.Marshal(map[string]string{"error": jsonErr.Error()})
	}
	return string(b)
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
		"version":      "0.2.0",
		"hasFormat":    true,
		"hasVet":       true,
		"hasOS":        true,
		"hasAst":       true,
		"hasBench":     true,
		"hasTrace":     true,
		"hasStats":     true,
		"hasCallGraph": true,
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
	js.Global().Set("nanoGoAst", js.FuncOf(jsNanoGoAst))
	js.Global().Set("nanoGoCallGraph", js.FuncOf(jsNanoGoCallGraph))
	js.Global().Set("nanoGoBench", js.FuncOf(jsNanoGoBench))
	js.Global().Set("nanoGoFormat", js.FuncOf(jsNanoGoFormat))
	js.Global().Set("nanoGoVet", js.FuncOf(jsNanoGoVet))
	js.Global().Set("nanoGoVersion", js.FuncOf(jsNanoGoVersion))
	js.Global().Set("nanoGoSetCanvas", js.FuncOf(jsNanoGoSetCanvas))
	js.Global().Set("nanoGoSetScale", js.FuncOf(jsNanoGoSetScale))

	// Signal readiness to the host (worker or main thread). The worker
	// installs `nanoGoSignalReady` before invoking go.run, so this call
	// resolves the worker's ready promise without any polling.
	if signal := js.Global().Get("nanoGoSignalReady"); signal.Type() == js.TypeFunction {
		signal.Invoke()
	}

	// Block forever for the browser event loop.
	select {}
}
