// cmd/wasm/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"syscall/js"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
	"simonwaldherr.de/go/nanogo/runtime"
)

// activeCanvas holds the currently bound canvas from the host page.
var activeCanvas runtime.CanvasBinding

// newPlaygroundVM builds an interpreter configured exactly like the
// playground expects: private in-memory VFS, HTTP allowed (still subject to
// browser CORS), and all curated packages registered.
func newPlaygroundVM() *interp.Interpreter {
	vm := interp.NewInterpreter()
	// Visual simulations intentionally render many small cells across dozens
	// or hundreds of frames. Keep the interpreter's conservative default for
	// embedded hosts, but give this explicit, user-operated playground a
	// larger deterministic budget so its shipped demos can finish. The worker
	// still has Stop and each run gets an isolated interpreter.
	vm.Limits.MaxSteps = 50_000_000
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
// an optional bounded trace timeline, and an optional per-line hit-count
// profile (see profileToJSON) for a "how often was this line executed"
// heatmap.
type runStats struct {
	ElapsedMs float64                   `json:"elapsedMs"`
	Steps     uint64                    `json:"steps"`
	Error     string                    `json:"error,omitempty"`
	Trace     []traceEventJSON          `json:"trace,omitempty"`
	TraceCap  int                       `json:"traceCap,omitempty"`
	Profile   []lineHit                 `json:"profile,omitempty"`
	Variables []interp.VariableSnapshot `json:"variables,omitempty"`
	Workspace *workspaceInfo            `json:"workspace,omitempty"`
}

// lineHit is one entry of a line-execution profile: how many times line's
// statements were reached. See interp.LineProfile.
type lineHit struct {
	Line  int    `json:"line"`
	Count uint64 `json:"count"`
}

// profileToJSON snapshots profile into a line-ascending slice — a shape
// that's both compact JSON and already in the order the UI wants to walk
// it (top of file to bottom) without needing to sort client-side.
func profileToJSON(profile *interp.LineProfile) []lineHit {
	if profile == nil {
		return nil
	}
	counts := profile.Counts()
	if len(counts) == 0 {
		return nil
	}
	hits := make([]lineHit, 0, len(counts))
	for line, count := range counts {
		hits = append(hits, lineHit{Line: line, Count: count})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Line < hits[j].Line })
	return hits
}

// workspaceFile is the small, path-safe file shape accepted by the browser
// SDK. Files are copied into a fresh VFS for every request; no host filesystem
// is exposed to guest code and no workspace state leaks between runs.
type workspaceFile struct {
	Path   string
	Source string
}

type workspacePackage struct {
	Dir     string   `json:"dir"`
	Name    string   `json:"name"`
	Files   []string `json:"files,omitempty"`
	Imports []string `json:"imports,omitempty"`
}

// workspaceInfo is returned with a workspace run and by the check endpoint.
// It gives IDEs enough information for a project tree/package graph without
// having to duplicate loader resolution in JavaScript.
type workspaceInfo struct {
	Module   string             `json:"module"`
	Root     string             `json:"root"`
	Entry    string             `json:"entry"`
	Files    int                `json:"files"`
	Packages []workspacePackage `json:"packages"`
}

func normalizeWorkspacePath(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || strings.ContainsRune(raw, 0) || strings.HasPrefix(raw, "/") {
		return "", false
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	if path.Base(clean) != "go.mod" && !strings.HasSuffix(clean, ".go") {
		return "", false
	}
	return clean, true
}

func workspaceFilesFromJS(value js.Value) ([]workspaceFile, error) {
	if value.Type() != js.TypeObject || value.IsNull() {
		return nil, nil
	}
	length := value.Length()
	files := make([]workspaceFile, 0, length)
	seen := make(map[string]struct{}, length)
	for i := 0; i < length; i++ {
		item := value.Index(i)
		if item.Type() != js.TypeObject || item.IsNull() {
			continue
		}
		name, ok := normalizeWorkspacePath(item.Get("path").String())
		if !ok {
			return nil, fmt.Errorf("invalid workspace path at index %d", i)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate workspace path %q", name)
		}
		seen[name] = struct{}{}
		files = append(files, workspaceFile{Path: name, Source: item.Get("source").String()})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("workspace has no Go source files")
	}
	return files, nil
}

func loadWorkspace(vm *interp.Interpreter, files []workspaceFile, modulePath string) (*loader.Program, error) {
	const root = "/workspace"
	if err := vm.VFS.RemoveAll(root); err != nil {
		return nil, err
	}
	if err := vm.VFS.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	for _, file := range files {
		target := path.Join(root, file.Path)
		if err := vm.VFS.MkdirAll(path.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err := vm.VFS.WriteFile(target, []byte(file.Source), 0644); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(modulePath) == "" {
		modulePath = "nanogo.local/workspace"
	}
	return loader.LoadModule(vm.VFS, root, loader.Options{ModulePath: modulePath})
}

func workspaceInfoFromProgram(prog *loader.Program, fileCount int) *workspaceInfo {
	if prog == nil {
		return nil
	}
	info := &workspaceInfo{
		Module: prog.ModulePath, Root: prog.Root, Entry: prog.Entry,
		Files: fileCount, Packages: make([]workspacePackage, 0, len(prog.Order)),
	}
	for _, dir := range prog.Order {
		pkg := prog.Packages[dir]
		if pkg == nil {
			continue
		}
		entry := workspacePackage{Dir: dir, Name: pkg.Name}
		for _, file := range pkg.Files {
			filename := pkg.FSet.Position(file.Pos()).Filename
			filename = strings.TrimPrefix(filename, prog.Root+"/")
			entry.Files = append(entry.Files, filename)
		}
		for _, imp := range pkg.Imports {
			entry.Imports = append(entry.Imports, imp.Path)
		}
		sort.Strings(entry.Files)
		sort.Strings(entry.Imports)
		info.Packages = append(info.Packages, entry)
	}
	return info
}

func workspaceFilesValue(args []js.Value) ([]workspaceFile, string, error) {
	if len(args) < 1 {
		return nil, "", fmt.Errorf("missing workspace files")
	}
	files, err := workspaceFilesFromJS(args[0])
	if err != nil {
		return nil, "", err
	}
	modulePath := "nanogo.local/workspace"
	if len(args) >= 2 && args[1].Type() == js.TypeString {
		modulePath = strings.TrimSpace(args[1].String())
	}
	return files, modulePath, nil
}

func fillTraceStats(stats *runStats, tracer *interp.Tracer, traceCapacity int) {
	if tracer == nil {
		return
	}
	events := tracer.Events()
	stats.TraceCap = traceCapacity
	stats.Trace = make([]traceEventJSON, 0, len(events))
	var epoch time.Time
	for i, ev := range events {
		if i == 0 {
			epoch = ev.At
		}
		stats.Trace = append(stats.Trace, traceEventJSON{
			Seq: ev.Sequence, Ms: float64(ev.At.Sub(epoch).Microseconds()) / 1000,
			Kind: ev.Kind, Fn: ev.Function, Msg: ev.Message,
			Line: ev.Location.Line, Col: ev.Location.Column,
		})
	}
}

func breakpointLinesFromJS(value js.Value) []int {
	if value.Type() != js.TypeObject || value.IsNull() {
		return nil
	}
	length := value.Length()
	if length <= 0 {
		return nil
	}
	lines := make([]int, 0, length)
	for i := 0; i < length; i++ {
		line := value.Index(i).Int()
		if line > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// jsNanoGoRun runs nanoGo on a source string coming from JS. An optional
// truthy second argument enables local tracing; an optional truthy third
// argument enables the line-execution profile. A fourth array argument
// configures source breakpoints; breakpoint hits are emitted into the trace.
// The result is a JSON string with elapsedMs/steps/error/trace/profile so
// hosts can surface run details.
func jsNanoGoRun(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		runtime.ConsoleError("nanoGoRun: missing source")
		return nil
	}
	source := args[0].String()
	wantTrace := len(args) >= 2 && args[1].Truthy()
	wantProfile := len(args) >= 3 && args[2].Truthy()
	var breakpointLines []int
	if len(args) >= 4 {
		breakpointLines = breakpointLinesFromJS(args[3])
	}
	if len(breakpointLines) > 0 {
		// Breakpoint events are useful only when the host asks for a trace. Do
		// this implicitly so setting a gutter marker is enough to activate the
		// debug signal on the next ordinary run.
		wantTrace = true
	}

	vm := newPlaygroundVM()
	variables := interp.NewVariableTracker()
	vm.SetVariableTracker(variables)
	vm.SetBreakpoints(breakpointLines)
	// A worker-side CanvasBinding batches cell updates to avoid one Go→JS
	// callback per cell. Ensure a program that omits CanvasFlush still paints
	// its final frame before control returns to the worker.
	defer activeCanvas.Flush()

	traceCapacity := 4096
	if len(breakpointLines) > 0 {
		traceCapacity = 16384
	}
	var tracer *interp.Tracer
	if wantTrace {
		tracer = interp.NewTracer(traceCapacity)
		vm.SetTracer(tracer)
	}
	var profile *interp.LineProfile
	if wantProfile {
		profile = interp.NewLineProfile()
		vm.SetLineProfile(profile)
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
	stats.Profile = profileToJSON(profile)
	stats.Variables = variables.Snapshots()

	b, jsonErr := json.Marshal(stats)
	if jsonErr != nil {
		return nil
	}
	return string(b)
}

// jsNanoGoRunWorkspace executes a small multi-file module from a fresh VFS.
// The browser sends source snapshots, never host paths; loader.LoadModule then
// applies the same go.mod/local-import rules used by native SDK hosts.
// Arguments: files [{path, source}], modulePath, trace, profile.
func jsNanoGoRunWorkspace(this js.Value, args []js.Value) any {
	files, modulePath, err := workspaceFilesValue(args)
	if err != nil {
		data, _ := json.Marshal(runStats{Error: err.Error()})
		return string(data)
	}
	wantTrace := len(args) >= 3 && args[2].Truthy()
	wantProfile := len(args) >= 4 && args[3].Truthy()

	vm := newPlaygroundVM()
	variables := interp.NewVariableTracker()
	vm.SetVariableTracker(variables)
	traceCapacity := 4096
	var tracer *interp.Tracer
	if wantTrace {
		tracer = interp.NewTracer(traceCapacity)
		vm.SetTracer(tracer)
	}
	var profile *interp.LineProfile
	if wantProfile {
		profile = interp.NewLineProfile()
		vm.SetLineProfile(profile)
	}

	stats := runStats{}
	prog, loadErr := loadWorkspace(vm, files, modulePath)
	if loadErr != nil {
		stats.Error = loadErr.Error()
		data, _ := json.Marshal(stats)
		return string(data)
	}
	stats.Workspace = workspaceInfoFromProgram(prog, len(files))
	start := time.Now()
	err = loader.RunProgram(context.Background(), vm, prog, "main")
	stats.ElapsedMs = float64(time.Since(start).Microseconds()) / 1000
	stats.Steps = vm.LastStepCount()
	if err != nil {
		stats.Error = err.Error()
		runtime.ConsoleError("nanoGo workspace error: " + err.Error())
	}
	activeCanvas.Flush()
	fillTraceStats(&stats, tracer, traceCapacity)
	stats.Profile = profileToJSON(profile)
	stats.Variables = variables.Snapshots()
	data, _ := json.Marshal(stats)
	return string(data)
}

type workspaceCheckResult struct {
	OK        bool           `json:"ok"`
	Error     string         `json:"error,omitempty"`
	Workspace *workspaceInfo `json:"workspace,omitempty"`
}

// jsNanoGoWorkspaceCheck parses and resolves a workspace without executing
// guest code. It is the browser equivalent of a lightweight `go list`/build
// check and is intentionally safe to call on every editor change.
func jsNanoGoWorkspaceCheck(this js.Value, args []js.Value) any {
	files, modulePath, err := workspaceFilesValue(args)
	if err != nil {
		data, _ := json.Marshal(workspaceCheckResult{Error: err.Error()})
		return string(data)
	}
	vm := newPlaygroundVM()
	prog, err := loadWorkspace(vm, files, modulePath)
	if err != nil {
		data, _ := json.Marshal(workspaceCheckResult{Error: err.Error()})
		return string(data)
	}
	data, _ := json.Marshal(workspaceCheckResult{OK: true, Workspace: workspaceInfoFromProgram(prog, len(files))})
	return string(data)
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
	// Profile is accumulated across every iteration (one shared LineProfile
	// reused for all N fresh interpreters), so a line inside a hot loop
	// shows N× the hits a cold one-off line does — the benchmark's own
	// repetition becomes signal for "which lines actually cost time" rather
	// than needing a separate profiling run.
	Profile []lineHit `json:"profile,omitempty"`
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
	wantProfile := len(args) >= 3 && args[2].Truthy()
	var profile *interp.LineProfile
	if wantProfile {
		profile = interp.NewLineProfile()
	}

	stats := benchStats{Iterations: n, RunsMs: make([]float64, 0, n)}
	var totalMs float64
	for i := 0; i < n; i++ {
		vm := newPlaygroundVM()
		if profile != nil {
			vm.SetLineProfile(profile)
		}
		start := time.Now()
		err := vm.Run(source)
		activeCanvas.Flush()
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
	stats.Profile = profileToJSON(profile)

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

type sourceTestCaseJSON struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Skipped  bool     `json:"skipped,omitempty"`
	Line     int      `json:"line"`
	Column   int      `json:"column"`
	Category string   `json:"category"`
	Messages []string `json:"messages,omitempty"`
}

type sourceTestResultJSON struct {
	Passed  bool                 `json:"passed"`
	Total   int                  `json:"total"`
	Failed  int                  `json:"failed"`
	Results []sourceTestCaseJSON `json:"results"`
}

func appendTestResults(result *sourceTestResultJSON, results []loader.TestResult) {
	for _, testResult := range results {
		result.Results = append(result.Results, sourceTestCaseJSON{
			Name: testResult.Name, Passed: testResult.Pass, Skipped: testResult.Skipped,
			Line: testResult.Line, Column: testResult.Column,
			Category: testResult.Category, Messages: testResult.Messages,
		})
		if !testResult.Pass {
			result.Failed++
		}
	}
}

// jsNanoGoTest runs TestXxx functions found in one editor document. Its
// optional second argument is a go test -run style regular-expression filter.
// The loader splits the document into a temporary VFS module, so this follows
// the same testing.T subset and result categories as multi-file MCP/CLI projects.
func jsNanoGoTest(this js.Value, args []js.Value) any {
	result := sourceTestResultJSON{}
	if len(args) < 1 {
		data, _ := json.Marshal(map[string]string{"error": "missing source"})
		return string(data)
	}
	vm := newPlaygroundVM()
	match := ""
	if len(args) > 1 {
		match = args[1].String()
	}
	results, err := loader.RunSourceTestsMatching(context.Background(), vm, args[0].String(), match)
	if err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(data)
	}
	result.Total = len(results)
	result.Results = make([]sourceTestCaseJSON, 0, len(results))
	appendTestResults(&result, results)
	result.Passed = result.Failed == 0
	data, _ := json.Marshal(result)
	return string(data)
}

// jsNanoGoTestWorkspace runs TestXxx functions across every package in a
// browser VFS snapshot. Each package still uses loader's isolated testing.T
// execution and the optional filter keeps go test -run semantics.
// Arguments: files [{path, source}], modulePath, filter.
func jsNanoGoTestWorkspace(this js.Value, args []js.Value) any {
	files, modulePath, err := workspaceFilesValue(args)
	if err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(data)
	}
	vm := newPlaygroundVM()
	prog, err := loadWorkspace(vm, files, modulePath)
	if err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(data)
	}
	match := ""
	if len(args) >= 3 && args[2].Type() == js.TypeString {
		match = args[2].String()
	}
	result := sourceTestResultJSON{}
	seen := make(map[string]struct{}, len(prog.Order))
	for _, dir := range prog.Order {
		pkg := prog.Packages[dir]
		if pkg == nil {
			continue
		}
		if _, ok := seen[pkg.Name]; ok {
			continue
		}
		seen[pkg.Name] = struct{}{}
		tests, testErr := loader.RunPackageTestsMatching(context.Background(), vm, prog, pkg.Name, match)
		if testErr != nil {
			data, _ := json.Marshal(map[string]string{"error": testErr.Error()})
			return string(data)
		}
		appendTestResults(&result, tests)
	}
	result.Total = len(result.Results)
	result.Passed = result.Failed == 0
	data, _ := json.Marshal(result)
	return string(data)
}

// jsNanoGoVersion returns a JSON object with version/capability information
// so the playground UI can detect which features are available.
func jsNanoGoVersion(this js.Value, args []js.Value) any {
	info := map[string]any{
		"version":           "0.3.0",
		"sdk":               "nanogo-sdk/0.3",
		"runtime":           "wasm",
		"hasFormat":         true,
		"hasVet":            true,
		"hasTests":          true,
		"hasOS":             true,
		"hasAst":            true,
		"hasBench":          true,
		"hasTrace":          true,
		"hasStats":          true,
		"hasCallGraph":      true,
		"hasProfile":        true,
		"hasWorkspace":      true,
		"hasModuleCheck":    true,
		"hasWorkspaceTests": true,
		"workspace": map[string]any{
			"multiFile":           true,
			"localImports":        true,
			"goMod":               true,
			"dynamicDependencies": "host-provided VFS snapshots only",
		},
		"limits": map[string]any{
			"maxSteps": 50_000_000,
			"trace":    "bounded ring",
			"network":  "browser CORS + capability",
		},
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
	js.Global().Set("nanoGoRunWorkspace", js.FuncOf(jsNanoGoRunWorkspace))
	js.Global().Set("nanoGoWorkspaceCheck", js.FuncOf(jsNanoGoWorkspaceCheck))
	js.Global().Set("nanoGoTestWorkspace", js.FuncOf(jsNanoGoTestWorkspace))
	js.Global().Set("nanoGoAst", js.FuncOf(jsNanoGoAst))
	js.Global().Set("nanoGoCallGraph", js.FuncOf(jsNanoGoCallGraph))
	js.Global().Set("nanoGoBench", js.FuncOf(jsNanoGoBench))
	js.Global().Set("nanoGoFormat", js.FuncOf(jsNanoGoFormat))
	js.Global().Set("nanoGoVet", js.FuncOf(jsNanoGoVet))
	js.Global().Set("nanoGoTest", js.FuncOf(jsNanoGoTest))
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
