// interp/packages.go
package interp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"path"
	"regexp"
	"sort"
	"strconv"
	strlib "strings"
	"sync"
	"text/template"
	"time"
	"unicode/utf8"
)

// RegisterBuiltinPackages installs a tiny, curated set of std-like packages:
// fmt, time, math, encoding/json, sync, regexp, strings, sort, strconv,
// math/rand, path, unicode/utf8, browser, text/template, http, fs, os,
// storage, testing, and debug. Each package intentionally exposes only the
// functions registered below; this is not a full Go standard library.
func RegisterBuiltinPackages(vm *Interpreter) {

	// --- fmt ---
	fmtPkg := &Package{Name: "fmt", Funcs: map[string]*Function{}}
	fmtPkg.Funcs["Println"] = &Function{Name: "Println", IsVariadic: true, Native: func(args []any) (any, error) {
		// Join with spaces + newline
		out := ""
		for i, a := range args {
			if i > 0 {
				out += " "
			}
			out += ToString(a)
		}
		// Reuse ConsoleLog via host
		if nfun, ok := vm.natives["ConsoleLog"]; ok {
			_, _ = nfun([]any{out})
		}
		return len(out), nil
	}}
	fmtPkg.Funcs["Printf"] = &Function{Name: "Printf", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return 0, nil
		}
		format := ToString(args[0])
		rest := args[1:]
		// Use host-provided sprintf wrapper to avoid re-implementing format parsing
		sp, ok := vm.natives["__hostSprintf"]
		if !ok {
			return 0, NewRuntimeError("host sprintf not available")
		}
		res, err := sp(append([]any{format}, rest...))
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
		rest := args[1:]
		sp, ok := vm.natives["__hostSprintf"]
		if !ok {
			return "", NewRuntimeError("host sprintf not available")
		}
		res, err := sp(append([]any{format}, rest...))
		if err != nil {
			return "", err
		}
		return ToString(res), nil
	}}
	vm.RegisterPackage("fmt", fmtPkg)

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
		duration := time.Duration(ToInt(args[0])) * time.Millisecond
		if duration < 0 {
			return nil, NewRuntimeError("time.Sleep: negative duration")
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

	// --- unicode/utf8 ---
	// The string-oriented subset complements strings without exposing host
	// state. Rune values are represented by nanoGo's integer values.
	utf8Pkg := &Package{Name: "unicode/utf8", Funcs: map[string]*Function{}, Vars: map[string]any{
		"RuneError": utf8.RuneError,
		"RuneSelf":  utf8.RuneSelf,
		"UTFMax":    utf8.UTFMax,
	}}
	utf8Pkg.Funcs["RuneCountInString"] = &Function{Name: "RuneCountInString", Params: []string{"s"}, Native: func(args []any) (any, error) {
		return utf8.RuneCountInString(ToString(args[0])), nil
	}}
	utf8Pkg.Funcs["RuneLen"] = &Function{Name: "RuneLen", Params: []string{"r"}, Native: func(args []any) (any, error) {
		return utf8.RuneLen(rune(ToInt(args[0]))), nil
	}}
	utf8Pkg.Funcs["ValidRune"] = &Function{Name: "ValidRune", Params: []string{"r"}, Native: func(args []any) (any, error) {
		return utf8.ValidRune(rune(ToInt(args[0]))), nil
	}}
	utf8Pkg.Funcs["ValidString"] = &Function{Name: "ValidString", Params: []string{"s"}, Native: func(args []any) (any, error) {
		return utf8.ValidString(ToString(args[0])), nil
	}}
	vm.RegisterPackage("unicode/utf8", utf8Pkg)

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

	// --- regexp --- (Compile -> *Regexp with methods)
	regexType := &TypeDef{Name: "Regexp", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[regexType.Name] = regexType
	regexType.Methods["MatchString"] = &Function{Name: "MatchString", RecvType: "Regexp", Params: []string{"s"}, Native: func(args []any) (any, error) {
		r := ensureNativeRegexp(args[0])
		return r.MatchString(ToString(args[1])), nil
	}}
	regexType.Methods["FindStringSubmatch"] = &Function{Name: "FindStringSubmatch", RecvType: "Regexp", Params: []string{"s"}, Native: func(args []any) (any, error) {
		r := ensureNativeRegexp(args[0])
		subs := r.FindStringSubmatch(ToString(args[1]))
		// Convert to []string slice value
		out := &SliceVal{ElementType: "string", Data: []any{}}
		for _, s := range subs {
			out.Data = append(out.Data, s)
		}
		return out, nil
	}}
	regPkg := &Package{Name: "regexp", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"Regexp": regexType}}
	regPkg.Funcs["Compile"] = &Function{Name: "Compile", Params: []string{"pattern"}, Native: func(args []any) (any, error) {
		r, err := regexp.Compile(ToString(args[0]))
		if err != nil {
			return nil, err
		}
		// Store native pointer in field "__native"
		return &StructVal{TypeName: "Regexp", Fields: map[string]any{"__native": r}}, nil
	}}
	vm.RegisterPackage("regexp", regPkg)

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

	// --- text/template (simple RenderString helper) ---
	tplPkg := &Package{Name: "text/template", Funcs: map[string]*Function{}}
	tplPkg.Funcs["RenderString"] = &Function{Name: "RenderString", Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		tmpl := ToString(args[0])
		var data any = nil
		if len(args) > 1 {
			data = args[1]
		}
		t, err := template.New("tpl").Parse(tmpl)
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

	// --- http (very simple: GetText, PostText) ---
	httpPkg := &Package{Name: "http", Funcs: map[string]*Function{}}
	httpPkg.Funcs["GetText"] = &Function{Name: "GetText", Params: []string{"url"}, Native: func(args []any) (any, error) {
		if len(args) == 0 {
			return "", NewRuntimeError("http.GetText: missing URL")
		}
		if err := vm.requireHTTP(ToString(args[0])); err != nil {
			return "", err
		}
		if n, ok := vm.hostNative("HTTPGetText"); ok {
			v, err := n([]any{ToString(args[0])})
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
		if err := vm.requireHTTP(ToString(args[0])); err != nil {
			return "", err
		}
		contentType := "application/json"
		if len(args) >= 3 {
			contentType = ToString(args[2])
		}
		if n, ok := vm.hostNative("HTTPPostText"); ok {
			v, err := n([]any{ToString(args[0]), ToString(args[1]), contentType})
			return v, err
		}
		return "", NewRuntimeError("HTTP host native not available")
	}}
	vm.RegisterPackage("http", httpPkg)

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

	// --- os (backed by VFS) ---
	registerOsPackage(vm)

	// --- testing (minimal *testing.T subset) ---
	registerTestingPackage(vm)
}

// nativeWaitGroup is intentionally context-aware. sync.WaitGroup.Wait cannot
// be selected with a context, which would otherwise let guest code make a
// timed-out execution wait forever.
type nativeWaitGroup struct {
	mu   sync.Mutex
	n    int
	done chan struct{}
}

func newNativeWaitGroup() *nativeWaitGroup {
	done := make(chan struct{})
	close(done)
	return &nativeWaitGroup{done: done}
}

func (w *nativeWaitGroup) Add(delta int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	previous := w.n
	next := previous + delta
	if next < 0 {
		return NewRuntimeError("sync: negative WaitGroup counter")
	}
	if previous == 0 && next > 0 {
		w.done = make(chan struct{})
	}
	w.n = next
	if previous > 0 && next == 0 {
		close(w.done)
	}
	return nil
}

func (w *nativeWaitGroup) Wait(ctx context.Context) error {
	w.mu.Lock()
	done := w.done
	w.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return contextError(ctx)
	}
}

// ensureNativeWG returns the nativeWaitGroup associated with a StructVal.
func ensureNativeWG(v any) *nativeWaitGroup {
	if sv, ok := v.(*StructVal); ok {
		if wgi, ok := sv.Fields["__native"]; ok {
			if wg, ok := wgi.(*nativeWaitGroup); ok {
				return wg
			}
		}
		wg := newNativeWaitGroup()
		sv.Fields["__native"] = wg
		return wg
	}
	return newNativeWaitGroup()
}

type nativeTimer struct {
	stop chan struct{}
	once sync.Once
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
	channel := &ChannelVal{ElementType: "int", C: make(chan any, 1)}
	timer := &nativeTimer{stop: make(chan struct{})}
	typeName := "Timer"
	if repeating {
		typeName = "Ticker"
	}
	value := &StructVal{TypeName: typeName, Fields: map[string]any{"C": channel, "__nativeTimer": timer}}
	duration := time.Duration(milliseconds) * time.Millisecond

	go func() {
		if !repeating {
			select {
			case <-time.After(duration):
				_, _ = channel.TrySend(int(time.Now().UnixMilli()))
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
		if timer, ok := value.Fields["__nativeTimer"].(*nativeTimer); ok {
			return timer.Stop()
		}
	}
	return false
}

// ensureNativeRegexp extracts the *regexp.Regexp from a StructVal.
func ensureNativeRegexp(v any) *regexp.Regexp {
	if sv, ok := v.(*StructVal); ok {
		if ri, ok := sv.Fields["__native"]; ok {
			if r, ok := ri.(*regexp.Regexp); ok {
				return r
			}
		}
	}
	return regexp.MustCompile("$") // matches empty string; fallback
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
func (vm *Interpreter) installImportedPackage(alias, path string) {
	switch path {
	case "fmt":
		if _, ok := vm.packages["fmt"]; !ok {
			RegisterBuiltinPackages(vm)
		} // idempotent
		vm.globals.Vars[alias] = vm.packages["fmt"]
	case "debug":
		if _, ok := vm.packages["debug"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.declare(alias, vm.packages["debug"], vm.globals)
	case "time":
		if _, ok := vm.packages["time"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["time"]
	case "math":
		if _, ok := vm.packages["math"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["math"]
	case "math/rand":
		if _, ok := vm.packages["math/rand"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["math/rand"]
	case "encoding/json":
		if _, ok := vm.packages["encoding/json"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["encoding/json"]
	case "json":
		if _, ok := vm.packages["encoding/json"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["encoding/json"]
	case "strings":
		if _, ok := vm.packages["strings"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["strings"]
	case "sort":
		if _, ok := vm.packages["sort"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["sort"]
	case "strconv":
		if _, ok := vm.packages["strconv"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["strconv"]
	case "path":
		if _, ok := vm.packages["path"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["path"]
	case "unicode/utf8":
		if _, ok := vm.packages["unicode/utf8"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["unicode/utf8"]
	case "sync":
		if _, ok := vm.packages["sync"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["sync"]
	case "regexp":
		if _, ok := vm.packages["regexp"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["regexp"]
	case "browser":
		if _, ok := vm.packages["browser"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["browser"]
	case "text/template":
		if _, ok := vm.packages["text/template"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["text/template"]
	case "http":
		if _, ok := vm.packages["http"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["http"]
	case "storage":
		if _, ok := vm.packages["storage"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["storage"]
	case "fs":
		if _, ok := vm.packages["fs"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["fs"]
	case "os":
		if _, ok := vm.packages["os"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["os"]
	case "testing":
		if _, ok := vm.packages["testing"]; !ok {
			RegisterBuiltinPackages(vm)
		}
		vm.globals.Vars[alias] = vm.packages["testing"]
	default:
		_ = fmt.Sprintf("unknown import: %s", path)
	}
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
		sv := &SliceVal{ElementType: "DirEntry", Data: []any{}}
		for _, e := range entries {
			sv.Data = append(sv.Data, &StructVal{TypeName: "DirEntry", Fields: map[string]any{
				"Name":  e.Name,
				"Size":  e.Size,
				"IsDir": e.IsDir,
				"Mode":  e.Mode,
			}})
		}
		return sv
	}

	osPkg := &Package{Name: "os", Funcs: map[string]*Function{}, Vars: map[string]any{}}

	// os.Args
	argsSlice := &SliceVal{ElementType: "string", Data: []any{}}
	for _, a := range vm.Args {
		argsSlice.Data = append(argsSlice.Data, a)
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
		var data []byte
		switch v := args[1].(type) {
		case []byte:
			data = v
		default:
			data = []byte(ToString(args[1]))
		}
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
		sv := &SliceVal{ElementType: "string", Data: []any{}}
		for _, p := range pairs {
			sv.Data = append(sv.Data, p)
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
