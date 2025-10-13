// interp/packages.go
package interp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"regexp"
	"sort"
	strlib "strings"
	"sync"
	"text/template"
	"time"
)

// RegisterBuiltinPackages installs a tiny, curated set of std-like packages:
// fmt, time, math, encoding/json, sync, regexp, strings, sort, math/rand, browser, text/template, http, storage.
func RegisterBuiltinPackages(vm *Interpreter) {

	// --- fmt ---
	fmtPkg := &Package{Name: "fmt", Funcs: map[string]*Function{}}
	fmtPkg.Funcs["Println"] = &Function{Name: "Println", IsVariadic: true, Native: func(args []any) (any, error) {
		// Join with spaces + newline
		out := ""
		for i, a := range args {
			if i > 0 { out += " " }
			out += ToString(a)
		}
		// Reuse ConsoleLog via host
		if nfun, ok := vm.natives["ConsoleLog"]; ok { _, _ = nfun([]any{out}) }
		return len(out), nil
	}}
	fmtPkg.Funcs["Printf"] = &Function{Name: "Printf", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 0 { return 0, nil }
		format := ToString(args[0])
		rest := args[1:]
		// Use host-provided sprintf wrapper to avoid re-implementing format parsing
		sp, ok := vm.natives["__hostSprintf"]
		if !ok { return 0, NewRuntimeError("host sprintf not available") }
		res, err := sp(append([]any{format}, rest...))
		if err != nil { return 0, err }
		out := ToString(res)
		if nfun, ok := vm.natives["ConsoleLog"]; ok { _, _ = nfun([]any{out}) }
		return len(out), nil
	}}
	fmtPkg.Funcs["Sprintf"] = &Function{Name: "Sprintf", IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) == 0 { return "", nil }
		format := ToString(args[0]); rest := args[1:]
		sp, ok := vm.natives["__hostSprintf"]; if !ok { return "", NewRuntimeError("host sprintf not available") }
		res, err := sp(append([]any{format}, rest...)); if err != nil { return "", err }
		return ToString(res), nil
	}}
	vm.RegisterPackage("fmt", fmtPkg)

	// --- time ---
	timePkg := &Package{Name: "time", Funcs: map[string]*Function{}, Vars: map[string]any{}}
	timePkg.Funcs["Now"] = &Function{Name: "Now", Native: func(args []any) (any, error) {
		return int(time.Now().UnixMilli()), nil
	}}
	timePkg.Funcs["Sleep"] = &Function{Name: "Sleep", Native: func(args []any) (any, error) {
		if len(args) > 0 { time.Sleep(time.Duration(ToInt(args[0])) * time.Millisecond) } // ms
		return nil, nil
	}}
	timePkg.Funcs["Since"] = &Function{Name: "Since", Native: func(args []any) (any, error) {
		if len(args) == 0 { return 0, nil }
		startMs := ToInt(args[0])
		return int(time.Since(time.UnixMilli(int64(startMs))).Milliseconds()), nil
	}}
	vm.RegisterPackage("time", timePkg)

	// --- math ---
	mathPkg := &Package{Name: "math", Funcs: map[string]*Function{}}
	mathPkg.Funcs["Sqrt"] = &Function{Name: "Sqrt", Native: func(args []any) (any, error) { return math.Sqrt(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Pow"] = &Function{Name: "Pow", Native: func(args []any) (any, error) { return math.Pow(ToFloat(args[0]), ToFloat(args[1])), nil }}
	mathPkg.Funcs["Sin"] = &Function{Name: "Sin", Native: func(args []any) (any, error) { return math.Sin(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Cos"] = &Function{Name: "Cos", Native: func(args []any) (any, error) { return math.Cos(ToFloat(args[0])), nil }}
	mathPkg.Funcs["Abs"] = &Function{Name: "Abs", Native: func(args []any) (any, error) { return math.Abs(ToFloat(args[0])), nil }}
	vm.RegisterPackage("math", mathPkg)

	// --- math/rand --- (small facade)
	randPkg := &Package{Name: "math/rand", Funcs: map[string]*Function{}}
	randPkg.Funcs["Intn"] = &Function{Name: "Intn", Params: []string{"n"}, Native: func(args []any) (any, error) {
		n := ToInt(args[0]); if n <= 0 { return 0, nil }
		return mrand.Intn(n), nil
	}}
	randPkg.Funcs["Seed"] = &Function{Name: "Seed", Params: []string{"seed"}, Native: func(args []any) (any, error) {
		mrand.Seed(int64(ToInt(args[0]))); return nil, nil
	}}
	vm.RegisterPackage("math/rand", randPkg)

	// --- encoding/json --- (very small facade)
	jsonPkg := &Package{Name: "encoding/json", Funcs: map[string]*Function{}}
	// Marshal(v any) -> string
	jsonPkg.Funcs["Marshal"] = &Function{Name: "Marshal", Native: func(args []any) (any, error) {
		if len(args) == 0 { return "null", nil }
		b, err := json.Marshal(args[0])
		if err != nil { return "", err }
		return string(b), nil
	}}
	// Unmarshal(s string) -> any   (NOTE: diverges from stdlib, returns value instead of filling a pointer)
	jsonPkg.Funcs["Unmarshal"] = &Function{Name: "Unmarshal", Native: func(args []any) (any, error) {
		if len(args) == 0 { return nil, nil }
		var v any
		err := json.Unmarshal([]byte(ToString(args[0])), &v)
		return v, err
	}}
	vm.RegisterPackage("encoding/json", jsonPkg)
	vm.RegisterPackage("json", jsonPkg) // convenience alias

	// --- strings --- (subset)
	stringsPkg := &Package{Name: "strings", Funcs: map[string]*Function{}}
	stringsPkg.Funcs["Contains"] = &Function{Name: "Contains", Params: []string{"s","sub"}, Native: func(args []any) (any, error) {
		return strlib.Contains(ToString(args[0]), ToString(args[1])), nil
	}}
	stringsPkg.Funcs["Split"] = &Function{Name: "Split", Params: []string{"s","sep"}, Native: func(args []any) (any, error) {
		parts := strlib.Split(ToString(args[0]), ToString(args[1]))
		out := &SliceVal{ElementType: "string", Data: []any{}}
		for _, p := range parts { out.Data = append(out.Data, p) }
		return out, nil
	}}
	stringsPkg.Funcs["Join"] = &Function{Name: "Join", Params: []string{"arr","sep"}, Native: func(args []any) (any, error) {
		arr, _ := args[0].(*SliceVal)
		sep := ToString(args[1])
		ss := make([]string, 0, len(arr.Data))
		for _, v := range arr.Data { ss = append(ss, ToString(v)) }
		return strlib.Join(ss, sep), nil
	}}
	stringsPkg.Funcs["ReplaceAll"] = &Function{Name: "ReplaceAll", Params: []string{"s","old","new"}, Native: func(args []any) (any, error) {
		return strlib.ReplaceAll(ToString(args[0]), ToString(args[1]), ToString(args[2])), nil
	}}
	stringsPkg.Funcs["ToUpper"] = &Function{Name: "ToUpper", Params: []string{"s"}, Native: func(args []any) (any, error) { return strlib.ToUpper(ToString(args[0])), nil }}
	stringsPkg.Funcs["ToLower"] = &Function{Name: "ToLower", Params: []string{"s"}, Native: func(args []any) (any, error) { return strlib.ToLower(ToString(args[0])), nil }}
	stringsPkg.Funcs["TrimSpace"] = &Function{Name: "TrimSpace", Params: []string{"s"}, Native: func(args []any) (any, error) { return strlib.TrimSpace(ToString(args[0])), nil }}
	vm.RegisterPackage("strings", stringsPkg)

	// --- sort --- (Ints only, in-place)
	sortPkg := &Package{Name: "sort", Funcs: map[string]*Function{}}
	sortPkg.Funcs["Ints"] = &Function{Name: "Ints", Params: []string{"slice"}, Native: func(args []any) (any, error) {
		s, ok := args[0].(*SliceVal); if !ok || s == nil { return nil, nil }
		sort.Slice(s.Data, func(i, j int) bool { return ToInt(s.Data[i]) < ToInt(s.Data[j]) })
		return nil, nil
	}}
	vm.RegisterPackage("sort", sortPkg)

	// --- sync.WaitGroup ---
	// We expose a struct type WaitGroup with methods Add/Done/Wait, backed by Go's sync.WaitGroup.
	wgType := &TypeDef{Name: "WaitGroup", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[wgType.Name] = wgType
	wgType.Methods["Add"] = &Function{Name: "Add", RecvType: "WaitGroup", Params: []string{"delta"}, Native: func(args []any) (any, error) {
		w := ensureNativeWG(args[0])
		delta := ToInt(args[1])
		w.Add(delta)
		return nil, nil
	}}
	wgType.Methods["Done"] = &Function{Name: "Done", RecvType: "WaitGroup", Native: func(args []any) (any, error) {
		w := ensureNativeWG(args[0]); w.Done(); return nil, nil
	}}
	wgType.Methods["Wait"] = &Function{Name: "Wait", RecvType: "WaitGroup", Native: func(args []any) (any, error) {
		w := ensureNativeWG(args[0]); w.Wait(); return nil, nil
	}}
	syncPkg := &Package{Name: "sync", Types: map[string]*TypeDef{"WaitGroup": wgType}}
	vm.RegisterPackage("sync", syncPkg)

	// --- regexp --- (Compile -> *Regexp with methods)
	regexType := &TypeDef{Name: "Regexp", Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
	vm.types[regexType.Name] = regexType
	regexType.Methods["MatchString"] = &Function{Name: "MatchString", RecvType: "Regexp", Params: []string{"s"}, Native: func(args []any) (any, error) {
		r := ensureNativeRegexp(args[0]); return r.MatchString(ToString(args[1])), nil
	}}
	regexType.Methods["FindStringSubmatch"] = &Function{Name: "FindStringSubmatch", RecvType: "Regexp", Params: []string{"s"}, Native: func(args []any) (any, error) {
		r := ensureNativeRegexp(args[0]); subs := r.FindStringSubmatch(ToString(args[1]))
		// Convert to []string slice value
		out := &SliceVal{ElementType: "string", Data: []any{}}
		for _, s := range subs { out.Data = append(out.Data, s) }
		return out, nil
	}}
	regPkg := &Package{Name: "regexp", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"Regexp": regexType}}
	regPkg.Funcs["Compile"] = &Function{Name: "Compile", Params: []string{"pattern"}, Native: func(args []any) (any, error) {
		r, err := regexp.Compile(ToString(args[0]))
		if err != nil { return nil, err }
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
				if i > 0 { out += " " }
				out += ToString(a)
			}
			_, _ = n([]any{out})
		}
		return nil, nil
	}}
	browserPkg.Funcs["ConsoleWarn"] = &Function{Name: "ConsoleWarn", IsVariadic: true, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["ConsoleWarn"]; ok { _, _ = n([]any{ToString(args[0])}) }
		return nil, nil
	}}
	browserPkg.Funcs["ConsoleError"] = &Function{Name: "ConsoleError", IsVariadic: true, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["ConsoleError"]; ok { _, _ = n([]any{ToString(args[0])}) }
		return nil, nil
	}}

	// DOM / Element helpers
	browserPkg.Funcs["SetHTML"] = &Function{Name: "SetHTML", Native: func(args []any) (any, error) {
		if len(args) >= 2 {
			if n, ok := vm.natives["SetInnerHTML"]; ok { _, _ = n([]any{ToString(args[0]), ToString(args[1])}) }
		}
		return nil, nil
	}}
	browserPkg.Funcs["GetHTML"] = &Function{Name: "GetHTML", Native: func(args []any) (any, error) {
		if len(args) >= 1 {
			if n, ok := vm.natives["GetInnerHTML"]; ok { v, _ := n([]any{ToString(args[0])}); return v, nil }
		}
		return "", nil
	}}
	browserPkg.Funcs["SetValue"] = &Function{Name: "SetValue", Native: func(args []any) (any, error) {
		if len(args) >= 2 { if n, ok := vm.natives["SetValue"]; ok { _, _ = n([]any{ToString(args[0]), ToString(args[1])}) } }
		return nil, nil
	}}
	browserPkg.Funcs["GetValue"] = &Function{Name: "GetValue", Native: func(args []any) (any, error) {
		if len(args) >= 1 { if n, ok := vm.natives["GetValue"]; ok { v, _ := n([]any{ToString(args[0])}); return v, nil } }
		return "", nil
	}}
	browserPkg.Funcs["AddClass"] = &Function{Name: "AddClass", Native: func(args []any) (any, error) {
		if len(args) >= 2 { if n, ok := vm.natives["AddClass"]; ok { _, _ = n([]any{ToString(args[0]), ToString(args[1])}) } }
		return nil, nil
	}}
	browserPkg.Funcs["RemoveClass"] = &Function{Name: "RemoveClass", Native: func(args []any) (any, error) {
		if len(args) >= 2 { if n, ok := vm.natives["RemoveClass"]; ok { _, _ = n([]any{ToString(args[0]), ToString(args[1])}) } }
		return nil, nil
	}}
	browserPkg.Funcs["Open"] = &Function{Name: "Open", Native: func(args []any) (any, error) {
		if len(args) >= 1 { if n, ok := vm.natives["OpenWindow"]; ok { _, _ = n([]any{ToString(args[0])}) } }
		return nil, nil
	}}
	browserPkg.Funcs["Alert"] = &Function{Name: "Alert", Native: func(args []any) (any, error) {
		if len(args) >= 1 { if n, ok := vm.natives["Alert"]; ok { _, _ = n([]any{ToString(args[0])}) } }
		return nil, nil
	}}

	// Canvas passthrough
	browserPkg.Funcs["CanvasSize"] = &Function{Name: "CanvasSize", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasSize"]; ok { _, _ = n(args) }
		return nil, nil
	}}
	browserPkg.Funcs["CanvasSet"] = &Function{Name: "CanvasSet", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasSet"]; ok { _, _ = n(args) }
		return nil, nil
	}}
	browserPkg.Funcs["CanvasFlush"] = &Function{Name: "CanvasFlush", Native: func(args []any) (any, error) {
		if n, ok := vm.natives["CanvasFlush"]; ok { _, _ = n(args) }
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
		if len(args) >= 2 { sel = ToString(args[1]) } else if sv, ok := args[0].(*StructVal); ok { if s, ok2 := sv.Fields["__sel"].(string); ok2 { sel = s } }
		if sel != "" {
			if n, ok := vm.natives["GetInnerHTML"]; ok { v, _ := n([]any{sel}); return v, nil }
		}
		return "", nil
	}}
	jqType.Methods["Html"] = &Function{Name: "Html", RecvType: "JQ", Params: []string{"sel","html"}, Native: func(args []any) (any, error) {
		sel := ""; html := ""
		if len(args) >= 3 { sel = ToString(args[1]); html = ToString(args[2]) } else if sv, ok := args[0].(*StructVal); ok { if s, ok2 := sv.Fields["__sel"].(string); ok2 { sel = s } }
		if sel != "" { if n, ok := vm.natives["SetInnerHTML"]; ok { _, _ = n([]any{sel, html}) } }
		return nil, nil
	}}
	jqType.Methods["Set"] = &Function{Name: "Set", RecvType: "JQ", Params: []string{"sel","val"}, Native: func(args []any) (any, error) {
		sel := ""; val := ""
		if len(args) >= 3 { sel = ToString(args[1]); val = ToString(args[2]) } else if sv, ok := args[0].(*StructVal); ok { if s, ok2 := sv.Fields["__sel"].(string); ok2 { sel = s } }
		if sel != "" { if n, ok := vm.natives["SetValue"]; ok { _, _ = n([]any{sel, val}) } }
		return nil, nil
	}}
	jqType.Methods["AddClass"] = &Function{Name: "AddClass", RecvType: "JQ", Params: []string{"sel","class"}, Native: func(args []any) (any, error) {
		sel := ""; cl := ""
		if len(args) >= 3 { sel = ToString(args[1]); cl = ToString(args[2]) } else if sv, ok := args[0].(*StructVal); ok { if s, ok2 := sv.Fields["__sel"].(string); ok2 { sel = s } }
		if sel != "" { if n, ok := vm.natives["AddClass"]; ok { _, _ = n([]any{sel, cl}) } }
		return nil, nil
	}}
	jqType.Methods["RemoveClass"] = &Function{Name: "RemoveClass", RecvType: "JQ", Params: []string{"sel","class"}, Native: func(args []any) (any, error) {
		sel := ""; cl := ""
		if len(args) >= 3 { sel = ToString(args[1]); cl = ToString(args[2]) } else if sv, ok := args[0].(*StructVal); ok { if s, ok2 := sv.Fields["__sel"].(string); ok2 { sel = s } }
		if sel != "" { if n, ok := vm.natives["RemoveClass"]; ok { _, _ = n([]any{sel, cl}) } }
		return nil, nil
	}}
	// On(event string, handler func()) is a no-op: we cannot register actual JS callbacks easily from the interpreter; leave as placeholder
	jqType.Methods["On"] = &Function{Name: "On", RecvType: "JQ", Params: []string{"sel","event"}, Native: func(args []any) (any, error) {
		return nil, nil
	}}

	// Provide global $ function
	browserPkg.Funcs["$"] = &Function{Name: "$", Params: []string{"sel"}, Native: func(args []any) (any, error) {
		// Return a struct value representing the selector; store selector in field "__sel"
		sel := ""
		if len(args) >= 1 { sel = ToString(args[0]) }
		sv := &StructVal{TypeName: "JQ", Fields: map[string]any{"__sel": sel}}
		return sv, nil
	}}

	vm.RegisterPackage("browser", browserPkg)

	// --- text/template (simple RenderString helper) ---
	tplPkg := &Package{Name: "text/template", Funcs: map[string]*Function{}}
	tplPkg.Funcs["RenderString"] = &Function{Name: "RenderString", Native: func(args []any) (any, error) {
		if len(args) == 0 { return "", nil }
		tmpl := ToString(args[0])
		var data any = nil
		if len(args) > 1 { data = args[1] }
		// Convert interpreter runtime values (MapVal, SliceVal, StructVal) into native Go types
		var convert func(any) any
		convert = func(v any) any {
			switch x := v.(type) {
			case *MapVal:
				out := map[string]any{}
				for h, vv := range x.Data {
					// original key may be stored in Keys map
					orig := x.Keys[h]
					keyStr := fmt.Sprintf("%v", orig)
					out[keyStr] = convert(vv)
				}
				return out
			case *SliceVal:
				arr := make([]any, len(x.Data))
				for i := range x.Data { arr[i] = convert(x.Data[i]) }
				return arr
			case *StructVal:
				out := map[string]any{}
				for k, vv := range x.Fields { out[k] = convert(vv) }
				return out
			default:
				return v
			}
		}
		t, err := template.New("tpl").Parse(tmpl)
		if err != nil { return "", err }
		var buf bytes.Buffer
		nativeData := convert(data)
		if err := t.Execute(&buf, nativeData); err != nil { return "", err }
		return buf.String(), nil
	}}
	vm.RegisterPackage("text/template", tplPkg)

	// --- http (very simple: GetText) ---
	httpPkg := &Package{Name: "http", Funcs: map[string]*Function{}}
	httpPkg.Funcs["GetText"] = &Function{Name: "GetText", Params: []string{"url"}, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["HTTPGetText"]; ok {
			v, err := n([]any{ToString(args[0])})
			return v, err
		}
		return "", nil
	}}
	vm.RegisterPackage("http", httpPkg)

	// --- storage (localStorage: SetItem/GetItem) ---
	storPkg := &Package{Name: "storage", Funcs: map[string]*Function{}}
	storPkg.Funcs["SetItem"] = &Function{Name: "SetItem", Params: []string{"key","value"}, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["LocalStorageSetItem"]; ok { _, _ = n([]any{ToString(args[0]), ToString(args[1])}) }
		return nil, nil
	}}
	storPkg.Funcs["GetItem"] = &Function{Name: "GetItem", Params: []string{"key"}, Native: func(args []any) (any, error) {
		if n, ok := vm.natives["LocalStorageGetItem"]; ok { v, _ := n([]any{ToString(args[0])}); return v, nil }
		return "", nil
	}}
	vm.RegisterPackage("storage", storPkg)
}

// ensureNativeWG returns the *sync.WaitGroup associated with a StructVal.
func ensureNativeWG(v any) *sync.WaitGroup {
	if sv, ok := v.(*StructVal); ok {
		if wgi, ok := sv.Fields["__native"]; ok {
			if wg, ok := wgi.(*sync.WaitGroup); ok { return wg }
		}
		wg := &sync.WaitGroup{}
		sv.Fields["__native"] = wg
		return wg
	}
	return &sync.WaitGroup{}
}

// ensureNativeRegexp extracts the *regexp.Regexp from a StructVal.
func ensureNativeRegexp(v any) *regexp.Regexp {
	if sv, ok := v.(*StructVal); ok {
		if ri, ok := sv.Fields["__native"]; ok {
			if r, ok := ri.(*regexp.Regexp); ok { return r }
		}
	}
	return regexp.MustCompile("$") // matches empty string; fallback
}

// resolvePackageSelector returns a function/type from a package if sel refers to a package member.
func (vm *Interpreter) resolvePackageSelector(pkg *Package, sel string) (any, bool) {
	if pkg == nil { return nil, false }
	if pkg.Funcs != nil {
		if f, ok := pkg.Funcs[sel]; ok { return f, true }
	}
	if pkg.Types != nil {
		if t, ok := pkg.Types[sel]; ok { return t, true }
	}
	if pkg.Vars != nil {
		if v, ok := pkg.Vars[sel]; ok { return v, true }
	}
	return nil, false
}

// installImportedPackage imports a package by name and binds it to an alias in globals.
func (vm *Interpreter) installImportedPackage(alias, path string) {
	switch path {
	case "fmt":
		if _, ok := vm.packages["fmt"]; !ok { RegisterBuiltinPackages(vm) } // idempotent
		vm.globals.Vars[alias] = vm.packages["fmt"]
	case "time":
		if _, ok := vm.packages["time"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["time"]
	case "math":
		if _, ok := vm.packages["math"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["math"]
	case "math/rand":
		if _, ok := vm.packages["math/rand"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["math/rand"]
	case "encoding/json":
		if _, ok := vm.packages["encoding/json"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["encoding/json"]
	case "json":
		if _, ok := vm.packages["encoding/json"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["encoding/json"]
	case "strings":
		if _, ok := vm.packages["strings"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["strings"]
	case "sort":
		if _, ok := vm.packages["sort"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["sort"]
	case "sync":
		if _, ok := vm.packages["sync"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["sync"]
	case "regexp":
		if _, ok := vm.packages["regexp"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["regexp"]
	case "browser":
		if _, ok := vm.packages["browser"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["browser"]
	case "text/template":
		if _, ok := vm.packages["text/template"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["text/template"]
	case "http":
		if _, ok := vm.packages["http"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["http"]
	case "storage":
		if _, ok := vm.packages["storage"]; !ok { RegisterBuiltinPackages(vm) }
		vm.globals.Vars[alias] = vm.packages["storage"]
	default:
		_ = fmt.Sprintf("unknown import: %s", path)
	}
}
