// interp/packages.go
package interp

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	mrand "math/rand"
	"path"
	"regexp"
	"sort"
	"strconv"
	strlib "strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"
	"unicode/utf8"
)

// builtinPackageBuilders maps every curated import path to the function that
// constructs that one package on an interpreter. Splitting construction per
// package is what lets it happen lazily: a host that calls
// RegisterBuiltinPackages no longer pays for all twenty, only for the ones a
// guest program actually imports. That matters because the per-interpreter
// sandbox, not the guest program, dominates the cost of a short run — and
// hosts like cmd/mcp build a fresh interpreter for every single tool call.
//
// Keep this in sync with BuiltinImportPaths (interp/imports.go) and with
// installImportedPackage's cases; TestEveryBuiltinImportPathResolves pins
// that they agree.
// It is filled in by init() rather than written as a composite literal here.
// Several builders reach guest evaluation (registerTestingPackage runs guest
// test functions), and evaluation reaches ensureBuiltinPackage, which reads
// this map — a literal would therefore be an initialization cycle that the
// compiler rejects. Assigning inside init() breaks the cycle without changing
// anything observable: init() still runs before any interpreter exists.
var builtinPackageBuilders map[string]func(*Interpreter)

func init() {
	builtinPackageBuilders = map[string]func(*Interpreter){
		"fmt":           registerFmtPackage,
		"debug":         registerDebugPackage,
		"time":          registerTimePackage,
		"math":          registerMathPackage,
		"math/rand":     registerRandPackage,
		"encoding/json": registerJSONPackage,
		"encoding/gob":  registerGobPackage,
		"protobuf":      registerProtobufPackage,
		"grpc":          registerGRPCPackage,
		// json is a convenience alias for encoding/json; the builder registers
		// the same *Package object under both names.
		"json":          registerJSONPackage,
		"errors":        registerErrorsPackage,
		"bytes":         registerBytesPackage,
		"strings":       registerStringsPackage,
		"sort":          registerSortPackage,
		"slices":        registerSlicesPackage,
		"strconv":       registerStrconvPackage,
		"path":          registerPathPackage,
		"unicode/utf8":  registerUTF8Package,
		"sync":          registerSyncPackage,
		"regexp":        registerRegexpPackage,
		"reflect":       registerReflectPackage,
		"runtime":       registerRuntimePackage,
		"runtime/debug": registerRuntimeDebugPackage,
		"browser":       registerBrowserPackage,
		"text/template": registerTemplatePackage,
		"http":          registerHTTPPackage,
		"fs":            registerFSPackage,
		"storage":       registerStoragePackage,
		"os":            registerOsPackage,
		"testing":       registerTestingPackage,
	}
}

// RegisterBuiltinPackages makes nanoGo's tiny, curated set of std-like
// packages available to guest code: fmt, time, math, encoding/json, sync,
// regexp, reflect, runtime, runtime/debug, strings, sort, strconv, math/rand,
// path, unicode/utf8, browser, text/template, http, fs, os, storage, testing,
// and debug. Each package
// intentionally exposes only the functions registered below; this is not a
// full Go standard library.
//
// The packages themselves are built on first use rather than here, so an
// interpreter only ever constructs the ones its guest program imports (see
// builtinPackageBuilders and ensureBuiltinPackage). This is invisible to
// callers: every path this function enables still resolves, whether the
// program reaches it through an import statement, through vm.Package, or
// through interp/loader's module resolution.
func RegisterBuiltinPackages(vm *Interpreter) {
	vm.builtinsEnabled = true
}

// ensureBuiltinPackage builds path's curated package if a host has enabled
// the builtins and it has not been constructed yet, then reports it. It is
// the single gate every builtin lookup passes through, so no caller has to
// know whether a package has been materialized.
func (vm *Interpreter) ensureBuiltinPackage(path string) (*Package, bool) {
	if pkg, ok := vm.packages[path]; ok {
		return pkg, true
	}
	if !vm.builtinsEnabled {
		return nil, false
	}
	build, ok := builtinPackageBuilders[path]
	if !ok {
		return nil, false
	}
	build(vm)
	pkg, ok := vm.packages[path]
	return pkg, ok
}

func registerFmtPackage(vm *Interpreter) {
	// --- fmt ---
	fmtPkg := &Package{Name: "fmt", Funcs: map[string]*Function{}}
	fmtPkg.Funcs["Println"] = &Function{Name: "Println", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 1 {
			message := ToString(args[0])
			if nfun, ok := vm.natives["ConsoleLog"]; ok {
				_, _ = nfun([]any{message})
			}
			return len(message), nil
		}
		// Join with spaces. A Builder avoids the quadratic copying caused by
		// repeated string concatenation in wide log/diagnostic lines.
		var out strlib.Builder
		for i, a := range args {
			if i > 0 {
				out.WriteByte(' ')
			}
			out.WriteString(ToString(a))
		}
		message := out.String()
		// Reuse ConsoleLog via host
		if nfun, ok := vm.natives["ConsoleLog"]; ok {
			_, _ = nfun([]any{message})
		}
		return len(message), nil
	}}
	fmtPkg.Funcs["Printf"] = &Function{Name: "Printf", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return 0, nil
		}
		format := ToString(args[0])
		// Use host-provided sprintf wrapper to avoid re-implementing format parsing
		sp, ok := vm.natives["__hostSprintf"]
		if !ok {
			return 0, NewRuntimeError("host sprintf not available")
		}
		res, err := callHostSprintf(sp, args, format)
		if err != nil {
			return 0, err
		}
		out := ToString(res)
		if nfun, ok := vm.natives["ConsoleLog"]; ok {
			_, _ = nfun([]any{out})
		}
		return len(out), nil
	}}
	fmtPkg.Funcs["Sprintf"] = &Function{Name: "Sprintf", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := ToString(args[0])
		sp, ok := vm.natives["__hostSprintf"]
		if !ok {
			return "", NewRuntimeError("host sprintf not available")
		}
		res, err := callHostSprintf(sp, args, format)
		if err != nil {
			return "", err
		}
		return ToString(res), nil
	}}
	vm.RegisterPackage("fmt", fmtPkg)
}

// callHostSprintf preserves the native formatting contract (the first value
// is always a Go string) without rebuilding the argument slice for normal
// guest format strings. A copied slice is only needed for unusual dynamic
// format values, so neither the caller's evaluated arguments nor a host
// native's retained slice can observe a mutation.
func callHostSprintf(sp func([]any) (any, error), args []any, format string) (any, error) {
	if _, ok := args[0].(string); ok {
		return sp(args)
	}
	formattedArgs := make([]any, len(args))
	copy(formattedArgs, args)
	formattedArgs[0] = format
	return sp(formattedArgs)
}

func registerDebugPackage(vm *Interpreter) {
	// --- debug ---
	// debug.Q, debug.Mark, debug.Stack, and debug.Vars are intercepted in
	// evalExpr so they can retain the original expression text or read the
	// caller's env/call frame — none of that is visible to a plain native,
	// whose signature only ever sees already-evaluated args. Their output
	// goes to the optional host-owned Tracer rather than guest stdout.
	debugPkg := &Package{Name: "debug", Funcs: map[string]*Function{}}
	debugPkg.Funcs["Q"] = &Function{Name: "Q", IsVariadic: true, Native: func([]any) (any, error) {
		return nil, NewRuntimeError("debug.Q must be called directly")
	}}
	debugPkg.Funcs["Mark"] = &Function{Name: "Mark", Params: []string{"label"}, Native: func([]any) (any, error) {
		return nil, NewRuntimeError("debug.Mark must be called directly")
	}}
	debugPkg.Funcs["Stack"] = &Function{Name: "Stack", Native: func([]any) (any, error) {
		return nil, NewRuntimeError("debug.Stack must be called directly")
	}}
	debugPkg.Funcs["Vars"] = &Function{Name: "Vars", Native: func([]any) (any, error) {
		return nil, NewRuntimeError("debug.Vars must be called directly")
	}}
	// debug.Assert needs no env/frame access — just its evaluated args — so
	// unlike the four above it runs as a normal native. A false condition
	// fails with msg (default "assertion failed") and is recorded on the
	// Tracer so a host can see which assertion tripped even without guest
	// stdout output.
	debugPkg.Funcs["Assert"] = &Function{Name: "Assert", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("debug.Assert: expected a condition")
		}
		cond, ok := args[0].(bool)
		if !ok {
			return nil, NewRuntimeError("debug.Assert: first argument must be bool")
		}
		if cond {
			return nil, nil
		}
		msg := "assertion failed"
		if len(args) > 1 {
			parts := make([]string, 0, len(args)-1)
			for _, a := range args[1:] {
				parts = append(parts, ToString(a))
			}
			msg = strlib.Join(parts, " ")
		}
		vm.emitTrace("debug_assert_fail", "debug.Assert", msg, nil)
		return nil, NewRuntimeError("debug.Assert: " + msg)
	}}
	vm.RegisterPackage("debug", debugPkg)
}

// registerErrorsPackage exposes a small, pure comparison/construction facade
// over Go's real error values. nanoGo has no fmt.Errorf(%w) wrapping of its
// own, so guest code cannot build a wrap chain directly — but Is/Unwrap still
// walk any wrap chain a host-constructed error already carries, and Is's
// identity check alone is what makes a package-level sentinel error
// (`var ErrNotFound = errors.New(...)`) useful for guest comparisons.
func registerErrorsPackage(vm *Interpreter) {
	errorsPkg := &Package{Name: "errors", Funcs: map[string]*Function{}}
	errorsPkg.Funcs["New"] = &Function{Name: "New", Params: []string{"text"}, Native: func(args []any) (any, error) {
		text := ""
		if len(args) > 0 {
			text = ToString(args[0])
		}
		return NewRuntimeError(text), nil
	}}
	errorsPkg.Funcs["Is"] = &Function{Name: "Is", Params: []string{"err", "target"}, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return false, nil
		}
		err, _ := args[0].(error)
		target, _ := args[1].(error)
		return stderrors.Is(err, target), nil
	}}
	errorsPkg.Funcs["Unwrap"] = &Function{Name: "Unwrap", Params: []string{"err"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, nil
		}
		err, ok := args[0].(error)
		if !ok {
			return nil, nil
		}
		unwrapped := stderrors.Unwrap(err)
		if unwrapped == nil {
			return nil, nil
		}
		return unwrapped, nil
	}}
	errorsPkg.Funcs["Join"] = &Function{Name: "Join", IsVariadic: true, Native: func(args []any) (any, error) {
		errs := make([]error, 0, len(args))
		for _, a := range args {
			if a == nil {
				continue
			}
			if e, ok := a.(error); ok {
				errs = append(errs, e)
			}
		}
		joined := stderrors.Join(errs...)
		if joined == nil {
			return nil, nil
		}
		return joined, nil
	}}
	vm.RegisterPackage("errors", errorsPkg)
}

func registerTimePackage(vm *Interpreter) {
	// --- time ---
	timerType := &TypeDef{Name: "Timer", Kind: "struct", Fields: []FieldDef{{Name: "C", Type: "chan int"}}, Methods: map[string]*Function{}}
	tickerType := &TypeDef{Name: "Ticker", Kind: "struct", Fields: []FieldDef{{Name: "C", Type: "chan int"}}, Methods: map[string]*Function{}}
	vm.types[timerType.Name] = timerType
	vm.types[tickerType.Name] = tickerType
	timerType.Methods["Stop"] = &Function{Name: "Stop", RecvType: "Timer", Native: func(args []any) (any, error) {
		return stopNativeTimer(args[0]), nil
	}}
	tickerType.Methods["Stop"] = &Function{Name: "Stop", RecvType: "Ticker", Native: func(args []any) (any, error) {
		stopNativeTimer(args[0])
		return nil, nil
	}}
	timePkg := &Package{Name: "time", Funcs: map[string]*Function{}, Vars: map[string]any{}, Types: map[string]*TypeDef{"Timer": timerType, "Ticker": tickerType}}
	timePkg.Funcs["Now"] = &Function{Name: "Now", Native: func(args []any) (any, error) {
		return int(time.Now().UnixMilli()), nil
	}}
	timePkg.Funcs["Sleep"] = &Function{Name: "Sleep", NativeContext: func(ctx context.Context, args []any) (any, error) {
		if len(args) == 0 {
			return nil, nil
		}
		duration, err := millisecondsDuration(ToInt(args[0]), "time.Sleep")
		if err != nil {
			return nil, err
		}
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil, nil
		case <-ctx.Done():
			return nil, contextError(ctx)
		}
	}}
	timePkg.Funcs["Since"] = &Function{Name: "Since", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return 0, nil
		}
		startMs := ToInt(args[0])
		return int(time.Since(time.UnixMilli(int64(startMs))).Milliseconds()), nil
	}}
	timePkg.Funcs["NewTimer"] = &Function{Name: "NewTimer", Params: []string{"milliseconds"}, NativeContext: func(ctx context.Context, args []any) (any, error) {
		milliseconds := 0
		if len(args) > 0 {
			milliseconds = ToInt(args[0])
		}
		return newNativeTimer(ctx, milliseconds, false)
	}}
	timePkg.Funcs["NewTicker"] = &Function{Name: "NewTicker", Params: []string{"milliseconds"}, NativeContext: func(ctx context.Context, args []any) (any, error) {
		milliseconds := 0
		if len(args) > 0 {
			milliseconds = ToInt(args[0])
		}
		return newNativeTimer(ctx, milliseconds, true)
	}}
	vm.RegisterPackage("time", timePkg)
}

func registerMathPackage(vm *Interpreter) {
	// --- math ---
	mathPkg := &Package{Name: "math", Funcs: map[string]*Function{}, Vars: map[string]any{}}
	mathPkg.Funcs["Sqrt"] = &Function{Name: "Sqrt", Native: func(args []any) (any, error) { return math.Sqrt(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Pow"] = &Function{Name: "Pow", Native: func(args []any) (any, error) { return math.Pow(ToFloat(args[0]), ToFloat(args[1])), nil }}
	mathPkg.Funcs["Sin"] = &Function{Name: "Sin", Native: func(args []any) (any, error) { return math.Sin(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Cos"] = &Function{Name: "Cos", Native: func(args []any) (any, error) { return math.Cos(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Abs"] = &Function{Name: "Abs", Native: func(args []any) (any, error) { return math.Abs(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Floor"] = &Function{Name: "Floor", Native: func(args []any) (any, error) { return math.Floor(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Ceil"] = &Function{Name: "Ceil", Native: func(args []any) (any, error) { return math.Ceil(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Round"] = &Function{Name: "Round", Native: func(args []any) (any, error) { return math.Round(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Max"] = &Function{Name: "Max", Native: func(args []any) (any, error) { return math.Max(ToFloat(args[0]), ToFloat(args[1])), nil }}
	mathPkg.Funcs["Min"] = &Function{Name: "Min", Native: func(args []any) (any, error) { return math.Min(ToFloat(args[0]), ToFloat(args[1])), nil }}
	mathPkg.Funcs["Log"] = &Function{Name: "Log", Native: func(args []any) (any, error) { return math.Log(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Log2"] = &Function{Name: "Log2", Native: func(args []any) (any, error) { return math.Log2(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Log10"] = &Function{Name: "Log10", Native: func(args []any) (any, error) { return math.Log10(ToFloat(args[0])), nil }}
	mathPkg.Vars["Pi"] = math.Pi
	mathPkg.Vars["E"] = math.E
	vm.RegisterPackage("math", mathPkg)
}

func registerRandPackage(vm *Interpreter) {
	// --- math/rand --- (small facade)
	randPkg := &Package{Name: "math/rand", Funcs: map[string]*Function{}}
	randPkg.Funcs["Intn"] = &Function{Name: "Intn", Params: []string{"n"}, Native: func(args []any) (any, error) {
		n := ToInt(args[0])
		if n <= 0 {
			return 0, nil
		}
		return mrand.Intn(n), nil
	}}
	randPkg.Funcs["Seed"] = &Function{Name: "Seed", Params: []string{"seed"}, Native: func(args []any) (any, error) {
		mrand.Seed(int64(ToInt(args[0])))
		return nil, nil
	}}
	randPkg.Funcs["Float64"] = &Function{Name: "Float64", Native: func(args []any) (any, error) {
		return mrand.Float64(), nil
	}}
	vm.RegisterPackage("math/rand", randPkg)
}

func registerJSONPackage(vm *Interpreter) {
	// --- encoding/json --- (very small facade)
	jsonPkg := &Package{Name: "encoding/json", Funcs: map[string]*Function{}}
	// Marshal(v any) -> string
	jsonPkg.Funcs["Marshal"] = &Function{Name: "Marshal", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "null", nil
		}
		b, err := json.Marshal(ToNativeValue(args[0]))
		if err != nil {
			return "", err
		}
		return string(b), nil
	}}
	// Unmarshal(s string) -> any   (NOTE: diverges from stdlib, returns value instead of filling a pointer)
	jsonPkg.Funcs["Unmarshal"] = &Function{Name: "Unmarshal", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, nil
		}
		var v any
		err := json.Unmarshal([]byte(ToString(args[0])), &v)
		return v, err
	}}
	vm.RegisterPackage("encoding/json", jsonPkg)
	vm.RegisterPackage("json", jsonPkg) // convenience alias
}

// registerGobPackage exposes compact binary serialization for values that can
// cross nanoGo's host bridge. Unlike JSON this keeps a byte slice throughout
// the guest runtime, avoiding a binary-to-string round trip.
func registerGobPackage(vm *Interpreter) {
	gobPkg := &Package{Name: "encoding/gob", Funcs: map[string]*Function{}}
	gobPkg.Funcs["Encode"] = &Function{Name: "Encode", Params: []string{"value"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("gob.Encode: missing value")
		}
		var buffer bytes.Buffer
		if err := gob.NewEncoder(&buffer).Encode(gobEnvelope{Value: ToNativeValue(args[0])}); err != nil {
			return nil, err
		}
		return byteSliceValue(buffer.Bytes()), nil
	}}
	gobPkg.Funcs["Decode"] = &Function{Name: "Decode", Params: []string{"data"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("gob.Decode: missing data")
		}
		var envelope gobEnvelope
		if err := gob.NewDecoder(bytes.NewReader(binaryArg(args[0]))).Decode(&envelope); err != nil {
			return nil, err
		}
		return bridgeToGuest(envelope.Value)
	}}
	vm.RegisterPackage("encoding/gob", gobPkg)
}

// gobEnvelope makes Decode's dynamic result self-describing. Gob can only
// restore a value into an interface when the sender encoded an interface too.
type gobEnvelope struct{ Value any }

// registerProtobufPackage delegates schema-aware message handling to an
// explicit host adapter. Guest structs are dynamic and cannot implement
// proto.Message themselves; a host can pass generated messages opaquely and
// register ProtoMarshal/ProtoUnmarshal without exposing either primitive as a
// guest-callable bare identifier.
func registerProtobufPackage(vm *Interpreter) {
	protoPkg := &Package{Name: "protobuf", Funcs: map[string]*Function{}}
	protoPkg.Funcs["Marshal"] = hostBridgeFunction(vm, "ProtoMarshal", "protobuf.Marshal")
	protoPkg.Funcs["Unmarshal"] = hostBridgeFunction(vm, "ProtoUnmarshal", "protobuf.Unmarshal")
	vm.RegisterPackage("protobuf", protoPkg)
}

// registerGRPCPackage provides a cancellation-aware unary-call bridge. The
// host owns generated stubs, TLS and connection pooling; nanoGo only checks
// the target capability and forwards values without reflection or copies.
func registerGRPCPackage(vm *Interpreter) {
	grpcPkg := &Package{Name: "grpc", Funcs: map[string]*Function{}}
	grpcPkg.Funcs["Invoke"] = &Function{Name: "Invoke", Params: []string{"target", "method", "request"}, Native: func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, NewRuntimeError("grpc.Invoke: need target, method and request")
		}
		if err := vm.requireHTTP(nativeStringArg(args[0])); err != nil {
			return nil, err
		}
		native, ok := vm.hostNative("GRPCInvoke")
		if !ok {
			return nil, NewRuntimeError("grpc.Invoke: host bridge not available")
		}
		return native(args)
	}}
	vm.RegisterPackage("grpc", grpcPkg)
}

func hostBridgeFunction(vm *Interpreter, nativeName, operation string) *Function {
	return &Function{Name: operation, IsVariadic: true, Native: func(args []any) (any, error) {
		native, ok := vm.hostNative(nativeName)
		if !ok {
			return nil, NewRuntimeError(operation + ": host bridge not available")
		}
		return native(args)
	}}
}

func byteSliceValue(data []byte) *SliceVal {
	values := make([]any, len(data))
	for i, value := range data {
		values[i] = int(value)
	}
	return &SliceVal{ElementType: "byte", Data: values}
}

func binaryArg(value any) []byte {
	if data, ok := value.([]byte); ok {
		return data
	}
	if slice, ok := value.(*SliceVal); ok && isByteType(slice.ElementType) {
		data := make([]byte, len(slice.Data))
		for i, element := range slice.Data {
			data[i] = byte(ToInt(element))
		}
		return data
	}
	return []byte(ToString(value))
}

func registerStringsPackage(vm *Interpreter) {
	// --- strings --- (subset)
	stringsPkg := &Package{Name: "strings", Funcs: map[string]*Function{}}
	stringsPkg.Funcs["Contains"] = &Function{Name: "Contains", Params: []string{"s", "sub"}, Native: func(args []any) (any, error) {
		return strlib.Contains(ToString(args[0]), ToString(args[1])), nil
	}}
	stringsPkg.Funcs["Split"] = &Function{Name: "Split", Params: []string{"s", "sep"}, Native: func(args []any) (any, error) {
		parts := strlib.Split(ToString(args[0]), ToString(args[1]))
		out := &SliceVal{ElementType: "string", Data: []any{}}
		for _, p := range parts {
			out.Data = append(out.Data, p)
		}
		return out, nil
	}}
	stringsPkg.Funcs["Join"] = &Function{Name: "Join", Params: []string{"arr", "sep"}, Native: func(args []any) (any, error) {
		arr, _ := args[0].(*SliceVal)
		sep := ToString(args[1])
		ss := make([]string, 0, len(arr.Data))
		for _, v := range arr.Data {
			ss = append(ss, ToString(v))
		}
		return strlib.Join(ss, sep), nil
	}}
	stringsPkg.Funcs["ReplaceAll"] = &Function{Name: "ReplaceAll", Params: []string{"s", "old", "new"}, Native: func(args []any) (any, error) {
		return strlib.ReplaceAll(ToString(args[0]), ToString(args[1]), ToString(args[2])), nil
	}}
	stringsPkg.Funcs["Replace"] = &Function{Name: "Replace", Params: []string{"s", "old", "new", "n"}, Native: func(args []any) (any, error) {
		return strlib.Replace(ToString(args[0]), ToString(args[1]), ToString(args[2]), ToInt(args[3])), nil
	}}
	stringsPkg.Funcs["ToUpper"] = &Function{Name: "ToUpper", Params: []string{"s"}, Native: func(args []any) (any, error) { return strlib.ToUpper(ToString(args[0])), nil }}
	stringsPkg.Funcs["ToLower"] = &Function{Name: "ToLower", Params: []string{"s"}, Native: func(args []any) (any, error) { return strlib.ToLower(ToString(args[0])), nil }}
	stringsPkg.Funcs["TrimSpace"] = &Function{Name: "TrimSpace", Params: []string{"s"}, Native: func(args []any) (any, error) { return strlib.TrimSpace(ToString(args[0])), nil }}
	stringsPkg.Funcs["Trim"] = &Function{Name: "Trim", Params: []string{"s", "cutset"}, Native: func(args []any) (any, error) { return strlib.Trim(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["TrimPrefix"] = &Function{Name: "TrimPrefix", Params: []string{"s", "prefix"}, Native: func(args []any) (any, error) { return strlib.TrimPrefix(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["TrimSuffix"] = &Function{Name: "TrimSuffix", Params: []string{"s", "suffix"}, Native: func(args []any) (any, error) { return strlib.TrimSuffix(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["HasPrefix"] = &Function{Name: "HasPrefix", Params: []string{"s", "prefix"}, Native: func(args []any) (any, error) { return strlib.HasPrefix(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["HasSuffix"] = &Function{Name: "HasSuffix", Params: []string{"s", "suffix"}, Native: func(args []any) (any, error) { return strlib.HasSuffix(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["Count"] = &Function{Name: "Count", Params: []string{"s", "sub"}, Native: func(args []any) (any, error) { return strlib.Count(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["Index"] = &Function{Name: "Index", Params: []string{"s", "sub"}, Native: func(args []any) (any, error) { return strlib.Index(ToString(args[0]), ToString(args[1])), nil }}
	stringsPkg.Funcs["Repeat"] = &Function{Name: "Repeat", Params: []string{"s", "count"}, Native: func(args []any) (any, error) { return strlib.Repeat(ToString(args[0]), ToInt(args[1])), nil }}
	vm.RegisterPackage("strings", stringsPkg)
}

func registerSortPackage(vm *Interpreter) {
	// --- sort --- (Ints, Strings, Float64s in-place)
	sortPkg := &Package{Name: "sort", Funcs: map[string]*Function{}}
	sortPkg.Funcs["Ints"] = &Function{Name: "Ints", Params: []string{"slice"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return nil, nil
		}
		sort.Slice(s.Data, func(i, j int) bool { return ToInt(s.Data[i]) < ToInt(s.Data[j]) })
		return nil, nil
	}}
	sortPkg.Funcs["Strings"] = &Function{Name: "Strings", Params: []string{"slice"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return nil, nil
		}
		sort.Slice(s.Data, func(i, j int) bool { return ToString(s.Data[i]) < ToString(s.Data[j]) })
		return nil, nil
	}}
	sortPkg.Funcs["Float64s"] = &Function{Name: "Float64s", Params: []string{"slice"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return nil, nil
		}
		sort.Slice(s.Data, func(i, j int) bool { return ToFloat(s.Data[i]) < ToFloat(s.Data[j]) })
		return nil, nil
	}}
	vm.RegisterPackage("sort", sortPkg)
}

// genericLess orders two dynamically typed scalar values the same way
// sort.Ints/Strings/Float64s already do per-type, so slices.Sort/Max/Min
// agree with the rest of the interpreter's ordering rules rather than
// introducing a second comparison convention. Non-scalar elements (structs,
// slices, ...) have no natural order and always compare as not-less, the
// same "give up gracefully instead of panicking" choice equals() makes for
// uncomparable types.
func genericLess(a, b any) bool {
	switch x := a.(type) {
	case int:
		return x < ToInt(b)
	case float64:
		return x < ToFloat(b)
	case string:
		return x < ToString(b)
	default:
		return false
	}
}

// registerSlicesPackage exposes a small subset of Go's generic "slices"
// package. It reuses equals() (the same dynamic equality the == operator and
// reflect.DeepEqual use) so Contains/Index/Equal agree with the rest of the
// language instead of a separate notion of equality.
func registerSlicesPackage(vm *Interpreter) {
	slicesPkg := &Package{Name: "slices", Funcs: map[string]*Function{}}
	slicesPkg.Funcs["Contains"] = &Function{Name: "Contains", Params: []string{"s", "v"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return false, nil
		}
		for _, e := range s.Data {
			if equals(e, args[1]) {
				return true, nil
			}
		}
		return false, nil
	}}
	slicesPkg.Funcs["Index"] = &Function{Name: "Index", Params: []string{"s", "v"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return -1, nil
		}
		for i, e := range s.Data {
			if equals(e, args[1]) {
				return i, nil
			}
		}
		return -1, nil
	}}
	slicesPkg.Funcs["Equal"] = &Function{Name: "Equal", Params: []string{"a", "b"}, Native: func(args []any) (any, error) {
		a, aok := args[0].(*SliceVal)
		b, bok := args[1].(*SliceVal)
		if !aok || !bok {
			return a == b, nil
		}
		if len(a.Data) != len(b.Data) {
			return false, nil
		}
		for i := range a.Data {
			if !equals(a.Data[i], b.Data[i]) {
				return false, nil
			}
		}
		return true, nil
	}}
	slicesPkg.Funcs["Reverse"] = &Function{Name: "Reverse", Params: []string{"s"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return nil, nil
		}
		for i, j := 0, len(s.Data)-1; i < j; i, j = i+1, j-1 {
			s.Data[i], s.Data[j] = s.Data[j], s.Data[i]
		}
		return nil, nil
	}}
	slicesPkg.Funcs["Sort"] = &Function{Name: "Sort", Params: []string{"s"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil {
			return nil, nil
		}
		sort.Slice(s.Data, func(i, j int) bool { return genericLess(s.Data[i], s.Data[j]) })
		return nil, nil
	}}
	slicesPkg.Funcs["Max"] = &Function{Name: "Max", Params: []string{"s"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil || len(s.Data) == 0 {
			return nil, NewRuntimeError("slices.Max: empty slice")
		}
		best := s.Data[0]
		for _, e := range s.Data[1:] {
			if genericLess(best, e) {
				best = e
			}
		}
		return best, nil
	}}
	slicesPkg.Funcs["Min"] = &Function{Name: "Min", Params: []string{"s"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal)
		if !ok || s == nil || len(s.Data) == 0 {
			return nil, NewRuntimeError("slices.Min: empty slice")
		}
		best := s.Data[0]
		for _, e := range s.Data[1:] {
			if genericLess(e, best) {
				best = e
			}
		}
		return best, nil
	}}
	vm.RegisterPackage("slices", slicesPkg)
}

func registerStrconvPackage(vm *Interpreter) {
	// --- strconv ---
	strconvPkg := &Package{Name: "strconv", Funcs: map[string]*Function{}}
	strconvPkg.Funcs["Itoa"] = &Function{Name: "Itoa", Params: []string{"i"}, Native: func(args []any) (any, error) {
		return strconv.Itoa(ToInt(args[0])), nil
	}}
	strconvPkg.Funcs["Atoi"] = &Function{Name: "Atoi", Params: []string{"s"}, Native: func(args []any) (any, error) {
		n, err := strconv.Atoi(ToString(args[0]))
		if err != nil {
			return 0, err
		}
		return n, nil
	}}
	strconvPkg.Funcs["FormatFloat"] = &Function{Name: "FormatFloat", Params: []string{"f", "fmt", "prec", "bitSize"}, Native: func(args []any) (any, error) {
		if len(args) < 4 {
			return "", NewRuntimeError("FormatFloat: need 4 args")
		}
		// Accept a string format character (e.g., "f", "e", "g") or an int ASCII value.
		var fmtByte byte = 'f'
		switch fv := args[1].(type) {
		case string:
			if len(fv) > 0 {
				fmtByte = fv[0]
			}
		default:
			fmtByte = byte(ToInt(args[1]) & 0xFF)
		}
		return strconv.FormatFloat(ToFloat(args[0]), fmtByte, ToInt(args[2]), ToInt(args[3])), nil
	}}
	strconvPkg.Funcs["ParseFloat"] = &Function{Name: "ParseFloat", Params: []string{"s", "bitSize"}, Native: func(args []any) (any, error) {
		bitSize := 64
		if len(args) >= 2 {
			bitSize = ToInt(args[1])
		}
		f, err := strconv.ParseFloat(ToString(args[0]), bitSize)
		return f, err
	}}
	strconvPkg.Funcs["FormatBool"] = &Function{Name: "FormatBool", Params: []string{"b"}, Native: func(args []any) (any, error) {
		return strconv.FormatBool(ToBool(args[0])), nil
	}}
	strconvPkg.Funcs["ParseBool"] = &Function{Name: "ParseBool", Params: []string{"s"}, Native: func(args []any) (any, error) {
		b, err := strconv.ParseBool(ToString(args[0]))
		return b, err
	}}
	strconvPkg.Funcs["FormatInt"] = &Function{Name: "FormatInt", Params: []string{"i", "base"}, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return "", NewRuntimeError("FormatInt: need 2 args")
		}
		return strconv.FormatInt(int64(ToInt(args[0])), ToInt(args[1])), nil
	}}
	vm.RegisterPackage("strconv", strconvPkg)
}

func registerPathPackage(vm *Interpreter) {
	// --- path ---
	// path is purely lexical and therefore has the same behavior in native and
	// browser hosts. It is useful for URLs and virtual-filesystem paths without
	// granting access to either resource.
	pathPkg := &Package{Name: "path", Funcs: map[string]*Function{}}
	pathPkg.Funcs["Base"] = &Function{Name: "Base", Params: []string{"path"}, Native: func(args []any) (any, error) {
		return path.Base(ToString(args[0])), nil
	}}
	pathPkg.Funcs["Clean"] = &Function{Name: "Clean", Params: []string{"path"}, Native: func(args []any) (any, error) {
		return path.Clean(ToString(args[0])), nil
	}}
	pathPkg.Funcs["Dir"] = &Function{Name: "Dir", Params: []string{"path"}, Native: func(args []any) (any, error) {
		return path.Dir(ToString(args[0])), nil
	}}
	pathPkg.Funcs["Ext"] = &Function{Name: "Ext", Params: []string{"path"}, Native: func(args []any) (any, error) {
		return path.Ext(ToString(args[0])), nil
	}}
	pathPkg.Funcs["IsAbs"] = &Function{Name: "IsAbs", Params: []string{"path"}, Native: func(args []any) (any, error) {
		return path.IsAbs(ToString(args[0])), nil
	}}
	pathPkg.Funcs["Join"] = &Function{Name: "Join", IsVariadic: true, Native: func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = ToString(arg)
		}
		return path.Join(parts...), nil
	}}
	vm.RegisterPackage("path", pathPkg)
}

func registerUTF8Package(vm *Interpreter) {
	// --- unicode/utf8 ---
	// The string-oriented subset complements strings without exposing host
	// state. Rune values are represented by nanoGo's integer values.
	utf8Pkg := &Package{Name: "unicode/utf8", Funcs: map[string]*Function{}, Vars: map[string]any{
		"RuneError": utf8.RuneError,
		"RuneSelf":  utf8.RuneSelf,
		"UTFMax":    utf8.UTFMax,
	}}
	utf8Pkg.Funcs["RuneCountInString"] = &Function{Name: "RuneCountInString", Params: []string{"s"}, Native: func(args []any) (any, error) {
		return utf8.RuneCountInString(nativeStringArg(args[0])), nil
	}}
	utf8Pkg.Funcs["RuneLen"] = &Function{Name: "RuneLen", Params: []string{"r"}, Native: func(args []any) (any, error) {
		return utf8.RuneLen(rune(ToInt(args[0]))), nil
	}}
	utf8Pkg.Funcs["ValidRune"] = &Function{Name: "ValidRune", Params: []string{"r"}, Native: func(args []any) (any, error) {
		return utf8.ValidRune(rune(ToInt(args[0]))), nil
	}}
	utf8Pkg.Funcs["ValidString"] = &Function{Name: "ValidString", Params: []string{"s"}, Native: func(args []any) (any, error) {
		return utf8.ValidString(nativeStringArg(args[0])), nil
	}}
	vm.RegisterPackage("unicode/utf8", utf8Pkg)
}

// nativeStringArg is small enough to inline into native adapters. Guest
// source strings are already Go strings, so UTF-8 and regexp calls skip the
// broad dynamic conversion helper on their common path.
func nativeStringArg(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ToString(value)
}

func registerSyncPackage(vm *Interpreter) {
	// --- sync.WaitGroup ---
	// We expose a struct type WaitGroup with methods Add/Done/Wait, backed by Go's sync.WaitGroup.
	wgType := &TypeDef{Name: "WaitGroup", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[wgType.Name] = wgType
	wgType.Methods["Add"] = &Function{Name: "Add", RecvType: "WaitGroup", Params: []string{"delta"}, Native: func(args []any) (any, error) {
		w := ensureNativeWG(args[0])
		delta := ToInt(args[1])
		return nil, w.Add(delta)
	}}
	wgType.Methods["Done"] = &Function{Name: "Done", RecvType: "WaitGroup", Native: func(args []any) (any, error) {
		w := ensureNativeWG(args[0])
		return nil, w.Add(-1)
	}}
	wgType.Methods["Wait"] = &Function{Name: "Wait", RecvType: "WaitGroup", NativeContext: func(ctx context.Context, args []any) (any, error) {
		w := ensureNativeWG(args[0])
		return nil, w.Wait(ctx)
	}}
	syncPkg := &Package{Name: "sync", Types: map[string]*TypeDef{"WaitGroup": wgType}}
	vm.RegisterPackage("sync", syncPkg)
}

// registerBytesPackage exposes bytes.Buffer, the one bytes type worth a
// guest-visible facade: it's already an internal dependency of gob and
// text/template (see registerGobPackage/registerTemplatePackage), and its
// zero value is immediately usable, matching sync.WaitGroup's lazy-native
// pattern (a guest `var b bytes.Buffer` needs no explicit construction).
func registerBytesPackage(vm *Interpreter) {
	bufType := &TypeDef{Name: "Buffer", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[bufType.Name] = bufType
	bufType.Methods["Write"] = &Function{Name: "Write", RecvType: "Buffer", Params: []string{"data"}, Native: func(args []any) (any, error) {
		n, _ := ensureNativeBuffer(args[0]).Write(binaryArg(args[1]))
		return n, nil
	}}
	bufType.Methods["WriteString"] = &Function{Name: "WriteString", RecvType: "Buffer", Params: []string{"s"}, Native: func(args []any) (any, error) {
		n, _ := ensureNativeBuffer(args[0]).WriteString(ToString(args[1]))
		return n, nil
	}}
	bufType.Methods["WriteByte"] = &Function{Name: "WriteByte", RecvType: "Buffer", Params: []string{"b"}, Native: func(args []any) (any, error) {
		return nil, ensureNativeBuffer(args[0]).WriteByte(byte(ToInt(args[1])))
	}}
	bufType.Methods["String"] = &Function{Name: "String", RecvType: "Buffer", Native: func(args []any) (any, error) {
		return ensureNativeBuffer(args[0]).String(), nil
	}}
	bufType.Methods["Bytes"] = &Function{Name: "Bytes", RecvType: "Buffer", Native: func(args []any) (any, error) {
		return byteSliceValue(ensureNativeBuffer(args[0]).Bytes()), nil
	}}
	bufType.Methods["Len"] = &Function{Name: "Len", RecvType: "Buffer", Native: func(args []any) (any, error) {
		return ensureNativeBuffer(args[0]).Len(), nil
	}}
	bufType.Methods["Reset"] = &Function{Name: "Reset", RecvType: "Buffer", Native: func(args []any) (any, error) {
		ensureNativeBuffer(args[0]).Reset()
		return nil, nil
	}}
	bytesPkg := &Package{Name: "bytes", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"Buffer": bufType}}
	bytesPkg.Funcs["NewBuffer"] = &Function{Name: "NewBuffer", Params: []string{"buf"}, Native: func(args []any) (any, error) {
		var data []byte
		if len(args) > 0 {
			data = binaryArg(args[0])
		}
		value := &StructVal{TypeName: "Buffer", Fields: map[string]any{}}
		value.nativeState.Store(&structNativeState{value: bytes.NewBuffer(data)})
		return value, nil
	}}
	bytesPkg.Funcs["NewBufferString"] = &Function{Name: "NewBufferString", Params: []string{"s"}, Native: func(args []any) (any, error) {
		text := ""
		if len(args) > 0 {
			text = ToString(args[0])
		}
		value := &StructVal{TypeName: "Buffer", Fields: map[string]any{}}
		value.nativeState.Store(&structNativeState{value: bytes.NewBufferString(text)})
		return value, nil
	}}
	vm.RegisterPackage("bytes", bytesPkg)
}

// ensureNativeBuffer returns the *bytes.Buffer associated with a guest Buffer
// StructVal, lazily creating one for a zero-value receiver — mirrors
// ensureNativeWG's one-time, lock-guarded native-state transition.
func ensureNativeBuffer(v any) *bytes.Buffer {
	sv, ok := v.(*StructVal)
	if !ok {
		return &bytes.Buffer{}
	}
	if state := sv.nativeState.Load(); state != nil {
		if buf, ok := state.value.(*bytes.Buffer); ok {
			return buf
		}
	}
	sv.nativeMu.Lock()
	defer sv.nativeMu.Unlock()
	if state := sv.nativeState.Load(); state != nil {
		if buf, ok := state.value.(*bytes.Buffer); ok {
			return buf
		}
	}
	buf := &bytes.Buffer{}
	sv.nativeState.Store(&structNativeState{value: buf})
	return buf
}

func registerRegexpPackage(vm *Interpreter) {
	// --- regexp --- (Compile -> *Regexp with methods)
	regexType := &TypeDef{Name: "Regexp", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[regexType.Name] = regexType
	regexType.Methods["MatchString"] = &Function{Name: "MatchString", RecvType: "Regexp", Params: []string{"s"}, Native: func(args []any) (any, error) {
		r := ensureNativeRegexp(args[0])
		return r.MatchString(nativeStringArg(args[1])), nil
	}}
	regexType.Methods["FindStringSubmatch"] = &Function{Name: "FindStringSubmatch", RecvType: "Regexp", Params: []string{"s"}, Native: func(args []any) (any, error) {
		r := ensureNativeRegexp(args[0])
		subs := r.FindStringSubmatch(nativeStringArg(args[1]))
		// The regexp package has already sized the result exactly. Preserve
		// that shape in the guest container instead of growing an []any one
		// element at a time for every match.
		out := &SliceVal{ElementType: "string", Data: make([]any, len(subs))}
		for i, s := range subs {
			out.Data[i] = s
		}
		return out, nil
	}}
	regPkg := &Package{Name: "regexp", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"Regexp": regexType}}
	regPkg.Funcs["Compile"] = &Function{Name: "Compile", Params: []string{"pattern"}, Native: func(args []any) (any, error) {
		r, err := regexp.Compile(nativeStringArg(args[0]))
		if err != nil {
			return nil, err
		}
		// Keep the compatibility field for guest struct plumbing and publish
		// the same immutable regexp through StructVal's lock-free native slot.
		// Match methods then avoid a map lookup on every invocation.
		value := &StructVal{TypeName: "Regexp", Fields: map[string]any{"__native": r}}
		value.nativeState.Store(&structNativeState{value: r})
		return value, nil
	}}
	vm.RegisterPackage("regexp", regPkg)
}

func registerBrowserPackage(vm *Interpreter) {
	// --- browser ---
	browserPkg := &Package{Name: "browser", Funcs: map[string]*Function{}}
	// Console helpers
	browserPkg.Funcs["ConsoleLog"] = &Function{Name: "ConsoleLog", IsVariadic: true, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["ConsoleLog"]; ok {
			// join args
			out := ""
			for i, a := range args {
				if i > 0 {
					out += " "
				}
				out += ToString(a)
			}
			_, _ = n([]any{out})
		}
		return nil, nil
	}}
	browserPkg.Funcs["ConsoleWarn"] = &Function{Name: "ConsoleWarn", IsVariadic: true, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["ConsoleWarn"]; ok {
			_, _ = n([]any{ToString(args[0])})
		}
		return nil, nil
	}}
	browserPkg.Funcs["ConsoleError"] = &Function{Name: "ConsoleError", IsVariadic: true, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["ConsoleError"]; ok {
			_, _ = n([]any{ToString(args[0])})
		}
		return nil, nil
	}}

	// DOM / Element helpers
	browserPkg.Funcs["SetHTML"] = &Function{Name: "SetHTML", Native: func(args []any) (any, error) {
		if len(args) >= 2 {
			if n, ok := vm.natives["SetInnerHTML"]; ok {
				_, _ = n([]any{ToString(args[0]), ToString(args[1])})
			}
		}
		return nil, nil
	}}
	browserPkg.Funcs["GetHTML"] = &Function{Name: "GetHTML", Native: func(args []any) (any, error) {
		if len(args) >= 1 {
			if n, ok := vm.natives["GetInnerHTML"]; ok {
				v, _ := n([]any{ToString(args[0])})
				return v, nil
			}
		}
		return "", nil
	}}
	browserPkg.Funcs["SetValue"] = &Function{Name: "SetValue", Native: func(args []any) (any, error) {
		if len(args) >= 2 {
			if n, ok := vm.natives["SetValue"]; ok {
				_, _ = n([]any{ToString(args[0]), ToString(args[1])})
			}
		}
		return nil, nil
	}}
	browserPkg.Funcs["GetValue"] = &Function{Name: "GetValue", Native: func(args []any) (any, error) {
		if len(args) >= 1 {
			if n, ok := vm.natives["GetValue"]; ok {
				v, _ := n([]any{ToString(args[0])})
				return v, nil
			}
		}
		return "", nil
	}}
	browserPkg.Funcs["AddClass"] = &Function{Name: "AddClass", Native: func(args []any) (any, error) {
		if len(args) >= 2 {
			if n, ok := vm.natives["AddClass"]; ok {
				_, _ = n([]any{ToString(args[0]), ToString(args[1])})
			}
		}
		return nil, nil
	}}
	browserPkg.Funcs["RemoveClass"] = &Function{Name: "RemoveClass", Native: func(args []any) (any, error) {
		if len(args) >= 2 {
			if n, ok := vm.natives["RemoveClass"]; ok {
				_, _ = n([]any{ToString(args[0]), ToString(args[1])})
			}
		}
		return nil, nil
	}}
	browserPkg.Funcs["Open"] = &Function{Name: "Open", Native: func(args []any) (any, error) {
		if len(args) >= 1 {
			if n, ok := vm.natives["OpenWindow"]; ok {
				_, _ = n([]any{ToString(args[0])})
			}
		}
		return nil, nil
	}}
	browserPkg.Funcs["Alert"] = &Function{Name: "Alert", Native: func(args []any) (any, error) {
		if len(args) >= 1 {
			if n, ok := vm.natives["Alert"]; ok {
				_, _ = n([]any{ToString(args[0])})
			}
		}
		return nil, nil
	}}

	// Canvas passthrough
	browserPkg.Funcs["CanvasSize"] = &Function{Name: "CanvasSize", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasSize"]; ok {
			_, _ = n(args)
		}
		return nil, nil
	}}
	browserPkg.Funcs["CanvasSet"] = &Function{Name: "CanvasSet", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasSet"]; ok {
			_, _ = n(args)
		}
		return nil, nil
	}}
	browserPkg.Funcs["CanvasSetLevel"] = &Function{Name: "CanvasSetLevel", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasSetLevel"]; ok {
			_, _ = n(args)
		}
		return nil, nil
	}}
	browserPkg.Funcs["CanvasFlush"] = &Function{Name: "CanvasFlush", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasFlush"]; ok {
			_, _ = n(args)
		}
		return nil, nil
	}}

	vm.RegisterPackage("browser", browserPkg)

	// jQuery-like convenience: $ selector returning a tiny struct with methods
	// We represent the object as a struct with methods: Text, Html, Set, AddClass, RemoveClass, On
	jqType := &TypeDef{Name: "JQ", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[jqType.Name] = jqType
	jqType.Methods["Text"] = &Function{Name: "Text", RecvType: "JQ", Params: []string{"sel"}, Native: func(args []any) (any, error) {
		// args[0] receiver, args[1] selector
		sel := ""
		if len(args) >= 2 {
			sel = ToString(args[1])
		} else if sv, ok := args[0].(*StructVal); ok {
			if s, ok2 := sv.Fields["__sel"].(string); ok2 {
				sel = s
			}
		}
		if sel != "" {
			if n, ok := vm.natives["GetInnerHTML"]; ok {
				v, _ := n([]any{sel})
				return v, nil
			}
		}
		return "", nil
	}}
	jqType.Methods["Html"] = &Function{Name: "Html", RecvType: "JQ", Params: []string{"sel", "html"}, Native: func(args []any) (any, error) {
		sel := ""
		html := ""
		if len(args) >= 3 {
			sel = ToString(args[1])
			html = ToString(args[2])
		} else if sv, ok := args[0].(*StructVal); ok {
			if s, ok2 := sv.Fields["__sel"].(string); ok2 {
				sel = s
			}
		}
		if sel != "" {
			if n, ok := vm.natives["SetInnerHTML"]; ok {
				_, _ = n([]any{sel, html})
			}
		}
		return nil, nil
	}}
	jqType.Methods["Set"] = &Function{Name: "Set", RecvType: "JQ", Params: []string{"sel", "val"}, Native: func(args []any) (any, error) {
		sel := ""
		val := ""
		if len(args) >= 3 {
			sel = ToString(args[1])
			val = ToString(args[2])
		} else if sv, ok := args[0].(*StructVal); ok {
			if s, ok2 := sv.Fields["__sel"].(string); ok2 {
				sel = s
			}
		}
		if sel != "" {
			if n, ok := vm.natives["SetValue"]; ok {
				_, _ = n([]any{sel, val})
			}
		}
		return nil, nil
	}}
	jqType.Methods["AddClass"] = &Function{Name: "AddClass", RecvType: "JQ", Params: []string{"sel", "class"}, Native: func(args []any) (any, error) {
		sel := ""
		cl := ""
		if len(args) >= 3 {
			sel = ToString(args[1])
			cl = ToString(args[2])
		} else if sv, ok := args[0].(*StructVal); ok {
			if s, ok2 := sv.Fields["__sel"].(string); ok2 {
				sel = s
			}
		}
		if sel != "" {
			if n, ok := vm.natives["AddClass"]; ok {
				_, _ = n([]any{sel, cl})
			}
		}
		return nil, nil
	}}
	jqType.Methods["RemoveClass"] = &Function{Name: "RemoveClass", RecvType: "JQ", Params: []string{"sel", "class"}, Native: func(args []any) (any, error) {
		sel := ""
		cl := ""
		if len(args) >= 3 {
			sel = ToString(args[1])
			cl = ToString(args[2])
		} else if sv, ok := args[0].(*StructVal); ok {
			if s, ok2 := sv.Fields["__sel"].(string); ok2 {
				sel = s
			}
		}
		if sel != "" {
			if n, ok := vm.natives["RemoveClass"]; ok {
				_, _ = n([]any{sel, cl})
			}
		}
		return nil, nil
	}}
	// On(event string, handler func()) is a no-op: we cannot register actual JS callbacks easily from the interpreter; leave as placeholder
	jqType.Methods["On"] = &Function{Name: "On", RecvType: "JQ", Params: []string{"sel", "event"}, Native: func(args []any) (any, error) {
		return nil, nil
	}}

	// Provide global $ function
	browserPkg.Funcs["$"] = &Function{Name: "$", Params: []string{"sel"}, Native: func(args []any) (any, error) {
		// Return a struct value representing the selector; store selector in field "__sel"
		sel := ""
		if len(args) >= 1 {
			sel = ToString(args[0])
		}
		sv := &StructVal{TypeName: "JQ", Fields: map[string]any{"__sel": sel}}
		return sv, nil
	}}

	vm.RegisterPackage("browser", browserPkg)
}

// maxCachedTemplates bounds templateCache. Guest code that builds template
// text dynamically would otherwise grow the cache without limit, so once it
// is full the whole map is dropped and refilled rather than evicted entry by
// entry: templates are cheap to re-parse, and a plain reset keeps the hot
// path free of any bookkeeping (no LRU list, no access counters) while still
// adapting when a program moves on to a different set of templates.
const maxCachedTemplates = 64

// templateCache memoizes parsed templates by their source text. Templates are
// the one curated package where the same argument is overwhelmingly likely to
// arrive again and again — rendering a table, a report, or a page means
// running one template per row — and text/template's parse step costs far
// more than its execute step. A parsed *template.Template is immutable once
// built and safe to execute from several goroutines at once, so entries can be
// shared by every guest goroutine of the interpreter that owns the cache.
type templateCache struct {
	// snapshot is immutable after publication, letting repeated RenderString
	// calls avoid an RWMutex acquisition altogether. Rendering one template
	// per row is typically read-heavy, and guest goroutines may hit it in
	// parallel.
	snapshot atomic.Pointer[templateCacheSnapshot]
	mu       sync.Mutex // serializes cache misses and snapshot publication
}

type templateCacheSnapshot struct{ entries map[string]*template.Template }

// parse returns the compiled form of text, building and caching it on first
// use. A template that fails to parse is not cached: the error is cheap to
// reproduce, and caching failures would keep bad input alive for the life of
// the interpreter.
func (c *templateCache) parse(text string) (*template.Template, error) {
	if snapshot := c.snapshot.Load(); snapshot != nil {
		if cached, ok := snapshot.entries[text]; ok {
			return cached, nil
		}
	}
	compiled, err := template.New("tpl").Parse(text)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if snapshot := c.snapshot.Load(); snapshot != nil {
		if cached, ok := snapshot.entries[text]; ok {
			return cached, nil
		}
	}
	var entries map[string]*template.Template
	if previous := c.snapshot.Load(); previous != nil && len(previous.entries) < maxCachedTemplates {
		entries = make(map[string]*template.Template, len(previous.entries)+1)
		for key, value := range previous.entries {
			entries[key] = value
		}
	} else {
		entries = make(map[string]*template.Template, 8)
	}
	entries[text] = compiled
	c.snapshot.Store(&templateCacheSnapshot{entries: entries})
	return compiled, nil
}

func registerTemplatePackage(vm *Interpreter) {
	// --- text/template (simple RenderString helper) ---
	// The cache lives in this closure, so it is per-interpreter: a host that
	// builds one interpreter per request (cmd/mcp does) gets no cross-request
	// sharing, which is the conservative choice, while a single program's own
	// render loop — the case that actually matters — hits it every time.
	cache := &templateCache{}
	tplPkg := &Package{Name: "text/template", Funcs: map[string]*Function{}}
	tplPkg.Funcs["RenderString"] = &Function{Name: "RenderString", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		var data any = nil
		if len(args) > 1 {
			data = args[1]
		}
		t, err := cache.parse(ToString(args[0]))
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		nativeData := ToNativeValue(data)
		if err := t.Execute(&buf, nativeData); err != nil {
			return "", err
		}
		return buf.String(), nil
	}}
	vm.RegisterPackage("text/template", tplPkg)
}

func registerHTTPPackage(vm *Interpreter) {
	// --- http (very simple: GetText, PostText) ---
	httpPkg := &Package{Name: "http", Funcs: map[string]*Function{}}
	httpPkg.Funcs["GetText"] = &Function{Name: "GetText", Params: []string{"url"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", NewRuntimeError("http.GetText: missing URL")
		}
		rawURL := ToString(args[0])
		if err := vm.requireHTTP(rawURL); err != nil {
			return "", err
		}
		if n, ok := vm.hostNative("HTTPGetText"); ok {
			v, err := n([]any{rawURL})
			return v, err
		}
		return "", NewRuntimeError("HTTP host native not available")
	}}
	httpPkg.Funcs["PostText"] = &Function{Name: "PostText", IsVariadic: true, Native: func(args []any) (any, error) {
		// PostText(url, body [, contentType])
		// contentType defaults to "application/json" when omitted.
		if len(args) < 2 {
			return "", NewRuntimeError("http.PostText: missing URL or body")
		}
		rawURL := ToString(args[0])
		if err := vm.requireHTTP(rawURL); err != nil {
			return "", err
		}
		contentType := "application/json"
		if len(args) >= 3 {
			contentType = ToString(args[2])
		}
		if n, ok := vm.hostNative("HTTPPostText"); ok {
			v, err := n([]any{rawURL, ToString(args[1]), contentType})
			return v, err
		}
		return "", NewRuntimeError("HTTP host native not available")
	}}
	vm.RegisterPackage("http", httpPkg)
}

func registerFSPackage(vm *Interpreter) {
	// --- fs (read-only, host-proxied) ---
	fsPkg := &Package{Name: "fs", Funcs: map[string]*Function{}}
	fsPkg.Funcs["ReadFile"] = &Function{Name: "ReadFile", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", NewRuntimeError("fs.ReadFile: missing path")
		}
		filePath, err := vm.requireFileRead("fs.ReadFile", ToString(args[0]))
		if err != nil {
			return "", err
		}
		if n, ok := vm.hostNative("HostReadFile"); ok {
			v, err := n([]any{filePath})
			return v, err
		}
		return "", NewRuntimeError("host readfile not available")
	}}
	vm.RegisterPackage("fs", fsPkg)
}

func registerStoragePackage(vm *Interpreter) {
	// --- storage (localStorage: SetItem/GetItem) ---
	storPkg := &Package{Name: "storage", Funcs: map[string]*Function{}}
	storPkg.Funcs["SetItem"] = &Function{Name: "SetItem", Params: []string{"key", "value"}, Native: func(args []any) (any, error) {
		if n, ok := vm.hostNative("LocalStorageSetItem"); ok {
			_, _ = n([]any{ToString(args[0]), ToString(args[1])})
		}
		return nil, nil
	}}
	storPkg.Funcs["GetItem"] = &Function{Name: "GetItem", Params: []string{"key"}, Native: func(args []any) (any, error) {
		if n, ok := vm.hostNative("LocalStorageGetItem"); ok {
			v, _ := n([]any{ToString(args[0])})
			return v, nil
		}
		return "", nil
	}}
	vm.RegisterPackage("storage", storPkg)
}

// nativeWaitGroup is intentionally context-aware. sync.WaitGroup.Wait cannot
// be selected with a context, which would otherwise let guest code make a
// timed-out execution wait forever.
type nativeWaitGroup struct {
	mu sync.Mutex
	n  int
	// doneState is published atomically whenever Add transitions from zero
	// to positive. Wait only needs the current generation's channel, so the
	// uncontended read path no longer takes the mutex on every Wait call.
	doneState atomic.Pointer[waitGroupDone]
}

type waitGroupDone struct{ ch chan struct{} }

func newNativeWaitGroup() *nativeWaitGroup {
	done := make(chan struct{})
	close(done)
	w := &nativeWaitGroup{}
	w.doneState.Store(&waitGroupDone{ch: done})
	return w
}

func (w *nativeWaitGroup) Add(delta int) error {
	w.mu.Lock()
	previous := w.n
	next := previous + delta
	if next < 0 {
		w.mu.Unlock()
		return NewRuntimeError("sync: negative WaitGroup counter")
	}
	if previous == 0 && next > 0 {
		w.doneState.Store(&waitGroupDone{ch: make(chan struct{})})
	}
	w.n = next
	if previous > 0 && next == 0 {
		close(w.doneState.Load().ch)
	}
	w.mu.Unlock()
	return nil
}

func (w *nativeWaitGroup) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := w.doneState.Load().ch
	ctxDone := ctx.Done()
	if ctxDone == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctxDone:
		return contextError(ctx)
	}
}

// ensureNativeWG returns the nativeWaitGroup associated with a StructVal.
func ensureNativeWG(v any) *nativeWaitGroup {
	if sv, ok := v.(*StructVal); ok {
		if state := sv.nativeState.Load(); state != nil {
			if wg, ok := state.value.(*nativeWaitGroup); ok {
				return wg
			}
		}
		// A zero-value WaitGroup can be shared by several guest goroutines
		// before its first Add. Serialize only this one-time map transition;
		// every later Add/Done/Wait takes the atomic fast path above.
		sv.nativeMu.Lock()
		defer sv.nativeMu.Unlock()
		if state := sv.nativeState.Load(); state != nil {
			if wg, ok := state.value.(*nativeWaitGroup); ok {
				return wg
			}
		}
		if wgi, ok := sv.Fields["__native"]; ok {
			if wg, ok := wgi.(*nativeWaitGroup); ok {
				sv.nativeState.Store(&structNativeState{value: wg})
				return wg
			}
		}
		wg := newNativeWaitGroup()
		if sv.Fields == nil {
			sv.Fields = make(map[string]any, 1)
		}
		sv.Fields["__native"] = wg
		sv.nativeState.Store(&structNativeState{value: wg})
		return wg
	}
	return newNativeWaitGroup()
}

type nativeTimer struct {
	stop chan struct{}
	once sync.Once
}

const maxDurationMilliseconds = int64(1<<63-1) / int64(time.Millisecond)

func millisecondsDuration(milliseconds int, operation string) (time.Duration, error) {
	if milliseconds < 0 {
		return 0, NewRuntimeError(operation + ": negative duration")
	}
	if int64(milliseconds) > maxDurationMilliseconds {
		return 0, NewRuntimeError(operation + ": duration exceeds maximum")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func (timer *nativeTimer) Stop() bool {
	stopped := false
	timer.once.Do(func() {
		close(timer.stop)
		stopped = true
	})
	return stopped
}

func newNativeTimer(ctx context.Context, milliseconds int, repeating bool) (*StructVal, error) {
	if milliseconds <= 0 {
		return nil, NewRuntimeError("timer duration must be positive")
	}
	operation := "time.NewTimer"
	if repeating {
		operation = "time.NewTicker"
	}
	duration, err := millisecondsDuration(milliseconds, operation)
	if err != nil {
		return nil, err
	}
	channel := &ChannelVal{ElementType: "int", C: make(chan any, 1)}
	timer := &nativeTimer{stop: make(chan struct{})}
	typeName := "Timer"
	if repeating {
		typeName = "Ticker"
	}
	value := &StructVal{TypeName: typeName, Fields: map[string]any{"C": channel, "__nativeTimer": timer}}
	value.nativeState.Store(&structNativeState{value: timer})

	go func() {
		if !repeating {
			deadline := time.NewTimer(duration)
			defer deadline.Stop()
			select {
			case now := <-deadline.C:
				_, _ = channel.TrySend(int(now.UnixMilli()))
			case <-timer.stop:
			case <-ctx.Done():
			}
			return
		}

		ticker := time.NewTicker(duration)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				_, _ = channel.TrySend(int(now.UnixMilli()))
			case <-timer.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return value, nil
}

func stopNativeTimer(v any) bool {
	if value, ok := v.(*StructVal); ok {
		if state := value.nativeState.Load(); state != nil {
			if timer, ok := state.value.(*nativeTimer); ok {
				return timer.Stop()
			}
		}
		if timer, ok := value.Fields["__nativeTimer"].(*nativeTimer); ok {
			return timer.Stop()
		}
	}
	return false
}

// ensureNativeRegexp extracts the *regexp.Regexp from a StructVal.
func ensureNativeRegexp(v any) *regexp.Regexp {
	if sv, ok := v.(*StructVal); ok {
		if state := sv.nativeState.Load(); state != nil {
			if r, ok := state.value.(*regexp.Regexp); ok {
				return r
			}
		}
		if ri, ok := sv.Fields["__native"]; ok {
			if r, ok := ri.(*regexp.Regexp); ok {
				sv.nativeState.CompareAndSwap(nil, &structNativeState{value: r})
				return r
			}
		}
	}
	return regexp.MustCompile("$") // matches empty string; fallback
}

// packageForSelector resolves the left-hand identifier of a pkg.Member
// expression to a package. It looks in the caller's own scope first, so an
// import alias and a hot-swapped PackageScope both keep working, and only
// then materializes a curated builtin that this interpreter has not needed
// yet — which is what lets `fmt.Println` work with no import statement, the
// way it did when every builtin was registered up front (see
// examples/quickstart).
func (vm *Interpreter) packageForSelector(name string, env *Env) (*Package, bool) {
	if v, ok := vm.get(name, env); ok {
		pkg, isPackage := v.(*Package)
		// A non-package binding shadows the builtin, as it always has: the
		// name resolved, it just is not a package, so the caller falls
		// through to its method/field handling rather than reaching past a
		// local variable to a curated package of the same name.
		return pkg, isPackage
	}
	return vm.ensureBuiltinPackage(name)
}

// resolvePackageSelector returns a function/type from a package if sel refers to a package member.
func (vm *Interpreter) resolvePackageSelector(pkg *Package, sel string) (any, bool) {
	if pkg == nil {
		return nil, false
	}
	pkg.mu.RLock()
	defer pkg.mu.RUnlock()
	if pkg.Funcs != nil {
		if f, ok := pkg.Funcs[sel]; ok {
			return f, true
		}
	}
	if pkg.Types != nil {
		if t, ok := pkg.Types[sel]; ok {
			return t, true
		}
	}
	if pkg.Vars != nil {
		if v, ok := pkg.Vars[sel]; ok {
			return v, true
		}
	}
	return nil, false
}

// installImportedPackage imports a package by name and binds it to an alias in globals.
// installImportedPackage binds a curated package to alias in globals,
// building it first if this interpreter has not needed it yet.
//
// An import statement implies the builtins are in play on this interpreter
// even when the host never called RegisterBuiltinPackages, which is the
// long-standing behavior for plain Run/RunContext callers — so it enables
// them rather than refusing. An unrecognized path stays a silent no-op, also
// as before (see BuiltinImportPaths' note in interp/imports.go).
func (vm *Interpreter) installImportedPackage(alias, path string) {
	if !BuiltinImportPaths[path] {
		return
	}
	vm.builtinsEnabled = true
	pkg, ok := vm.ensureBuiltinPackage(path)
	if !ok {
		return
	}
	if path == "debug" {
		// debug goes through declare so it participates in normal scope
		// bookkeeping; every other alias is written straight into globals'
		// pre-allocated map.
		vm.declare(alias, pkg, vm.globals)
		return
	}
	vm.globals.Vars[alias] = pkg
}

// registerOsPackage installs a curated "os" package backed by the interpreter's VFS.
// It mirrors the most commonly used functions from the standard library os package.
func registerOsPackage(vm *Interpreter) {
	vfs := vm.VFS
	requireRead := func(operation, filePath string) (string, error) { return vm.requireFileRead(operation, filePath) }
	requireWrite := func(operation, filePath string) (string, error) { return vm.requireFileWrite(operation, filePath) }

	// Helper: build a *StructVal representing a FileInfo.
	fileInfoStruct := func(fi *VFSFileInfo) *StructVal {
		return &StructVal{TypeName: "FileInfo", Fields: map[string]any{
			"Name":  fi.Name,
			"Size":  fi.Size,
			"IsDir": fi.IsDir,
			"Mode":  fi.Mode,
		}}
	}

	// Helper: build a *SliceVal of DirEntry structs.
	dirEntrySlice := func(entries []*VFSFileInfo) *SliceVal {
		sv := &SliceVal{ElementType: "DirEntry", Data: make([]any, len(entries))}
		for i, e := range entries {
			sv.Data[i] = &StructVal{TypeName: "DirEntry", Fields: map[string]any{
				"Name":  e.Name,
				"Size":  e.Size,
				"IsDir": e.IsDir,
				"Mode":  e.Mode,
			}}
		}
		return sv
	}

	osPkg := &Package{Name: "os", Funcs: map[string]*Function{}, Vars: map[string]any{}}

	// os.Args
	argsSlice := &SliceVal{ElementType: "string", Data: make([]any, len(vm.Args))}
	for i, a := range vm.Args {
		argsSlice.Data[i] = a
	}
	osPkg.Vars["Args"] = argsSlice

	// os.Stdin / Stdout / Stderr — placeholder structs (no real I/O for now)
	osPkg.Vars["Stdin"] = &StructVal{TypeName: "File", Fields: map[string]any{"__fd": 0}}
	osPkg.Vars["Stdout"] = &StructVal{TypeName: "File", Fields: map[string]any{"__fd": 1}}
	osPkg.Vars["Stderr"] = &StructVal{TypeName: "File", Fields: map[string]any{"__fd": 2}}

	// os.ReadFile(path) (string, error)
	osPkg.Funcs["ReadFile"] = &Function{Name: "ReadFile", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", NewRuntimeError("ReadFile: missing path")
		}
		filePath, err := requireRead("os.ReadFile", ToString(args[0]))
		if err != nil {
			return "", err
		}
		data, err := vfs.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}}

	// os.WriteFile(path, data, perm)
	osPkg.Funcs["WriteFile"] = &Function{Name: "WriteFile", Params: []string{"path", "data", "perm"}, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, NewRuntimeError("WriteFile: missing args")
		}
		filePath, err := requireWrite("os.WriteFile", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		mode := 0644
		if len(args) >= 3 {
			mode = ToInt(args[2])
		}
		data := osWriteFileData(args[1])
		return nil, vfs.WriteFile(filePath, data, mode)
	}}

	// os.Mkdir(path, perm)
	osPkg.Funcs["Mkdir"] = &Function{Name: "Mkdir", Params: []string{"path", "perm"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("Mkdir: missing path")
		}
		filePath, err := requireWrite("os.Mkdir", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		mode := 0755
		if len(args) >= 2 {
			mode = ToInt(args[1])
		}
		return nil, vfs.Mkdir(filePath, mode)
	}}

	// os.MkdirAll(path, perm)
	osPkg.Funcs["MkdirAll"] = &Function{Name: "MkdirAll", Params: []string{"path", "perm"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("MkdirAll: missing path")
		}
		filePath, err := requireWrite("os.MkdirAll", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		mode := 0755
		if len(args) >= 2 {
			mode = ToInt(args[1])
		}
		return nil, vfs.MkdirAll(filePath, mode)
	}}

	// os.Remove(path)
	osPkg.Funcs["Remove"] = &Function{Name: "Remove", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("Remove: missing path")
		}
		filePath, err := requireWrite("os.Remove", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		return nil, vfs.Remove(filePath)
	}}

	// os.RemoveAll(path)
	osPkg.Funcs["RemoveAll"] = &Function{Name: "RemoveAll", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("RemoveAll: missing path")
		}
		filePath, err := requireWrite("os.RemoveAll", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		return nil, vfs.RemoveAll(filePath)
	}}

	// os.Stat(path) (*FileInfo, error)
	osPkg.Funcs["Stat"] = &Function{Name: "Stat", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("Stat: missing path")
		}
		filePath, err := requireRead("os.Stat", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		fi, err := vfs.Stat(filePath)
		if err != nil {
			return nil, err
		}
		return fileInfoStruct(fi), nil
	}}

	// os.ReadDir(path) ([]DirEntry, error)
	osPkg.Funcs["ReadDir"] = &Function{Name: "ReadDir", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("ReadDir: missing path")
		}
		filePath, err := requireRead("os.ReadDir", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		entries, err := vfs.ReadDir(filePath)
		if err != nil {
			return nil, err
		}
		return dirEntrySlice(entries), nil
	}}

	// os.Getenv(key) string
	osPkg.Funcs["Getenv"] = &Function{Name: "Getenv", Params: []string{"key"}, Native: func(args []any) (any, error) {
		if _, err := requireRead("os.Getenv", ""); err != nil {
			return "", err
		}
		if len(args) == 0 {
			return "", nil
		}
		return vfs.Getenv(ToString(args[0])), nil
	}}

	// os.Setenv(key, value) error
	osPkg.Funcs["Setenv"] = &Function{Name: "Setenv", Params: []string{"key", "value"}, Native: func(args []any) (any, error) {
		if _, err := requireWrite("os.Setenv", ""); err != nil {
			return nil, err
		}
		if len(args) < 2 {
			return nil, NewRuntimeError("Setenv: need key and value")
		}
		vfs.Setenv(ToString(args[0]), ToString(args[1]))
		return nil, nil
	}}

	// os.Environ() []string
	osPkg.Funcs["Environ"] = &Function{Name: "Environ", Native: func(args []any) (any, error) {
		if _, err := requireRead("os.Environ", ""); err != nil {
			return nil, err
		}
		pairs := vfs.Environ()
		sv := &SliceVal{ElementType: "string", Data: make([]any, len(pairs))}
		for i, p := range pairs {
			sv.Data[i] = p
		}
		return sv, nil
	}}

	// os.Getwd() (string, error)
	osPkg.Funcs["Getwd"] = &Function{Name: "Getwd", Native: func(args []any) (any, error) {
		return vfs.Getwd(), nil
	}}

	// os.Chdir(path) error
	osPkg.Funcs["Chdir"] = &Function{Name: "Chdir", Params: []string{"path"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return nil, NewRuntimeError("Chdir: missing path")
		}
		filePath, err := requireRead("os.Chdir", ToString(args[0]))
		if err != nil {
			return nil, err
		}
		return nil, vfs.Chdir(filePath)
	}}

	// os.TempDir() string
	osPkg.Funcs["TempDir"] = &Function{Name: "TempDir", Native: func(args []any) (any, error) {
		return "/tmp", nil
	}}

	// os.UserHomeDir() (string, error)
	osPkg.Funcs["UserHomeDir"] = &Function{Name: "UserHomeDir", Native: func(args []any) (any, error) {
		return "/home/user", nil
	}}

	// os.Exit(code) — signals exit via a panic so the interpreter unwinds
	osPkg.Funcs["Exit"] = &Function{Name: "Exit", Params: []string{"code"}, Native: func(args []any) (any, error) {
		code := 0
		if len(args) > 0 {
			code = ToInt(args[0])
		}
		return nil, NewRuntimeError(fmt.Sprintf("os.Exit(%d)", code))
	}}

	vm.RegisterPackage("os", osPkg)
}

// osWriteFileData converts the guest representation used by os.WriteFile
// directly to bytes. Byte slices used to take a detour through ToString,
// allocating both a temporary byte buffer and a string before VFS.WriteFile
// made its required ownership copy.
func osWriteFileData(value any) []byte {
	switch v := value.(type) {
	case []byte:
		return v
	case *SliceVal:
		if isByteType(v.ElementType) {
			out := make([]byte, len(v.Data))
			for i, element := range v.Data {
				out[i] = byte(ToInt(element))
			}
			return out
		}
	}
	return []byte(ToString(value))
}
