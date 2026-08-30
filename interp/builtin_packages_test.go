// interp/builtin_packages_test.go
package interp

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestTemplateCacheConcurrentParsePublishesOneEntry(t *testing.T) {
	cache := &templateCache{}
	const workers = 32
	start := make(chan struct{})
	results := make([]any, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = cache.parse("{{.Name}}")
		}(i)
	}
	close(start)
	wg.Wait()
	first := results[0]
	if first == nil || errs[0] != nil {
		t.Fatalf("first parse = %v, %v", first, errs[0])
	}
	for i := 1; i < workers; i++ {
		if errs[i] != nil || results[i] != first {
			t.Fatalf("worker %d = %v, %v; want cached template %v", i, results[i], errs[i], first)
		}
	}
	snapshot := cache.snapshot.Load()
	if snapshot == nil || len(snapshot.entries) != 1 {
		t.Fatalf("cache entries = %#v, want exactly one", snapshot)
	}
}

// Curated packages are constructed on first use rather than all at once by
// RegisterBuiltinPackages (see builtinPackageBuilders). These tests pin the
// contract that laziness has to preserve: every advertised import path still
// resolves, through each of the routes a host can take to reach one, and a
// package that was never asked for is genuinely not built.

// TestEveryBuiltinImportPathResolves checks BuiltinImportPaths against the
// builder registry in both directions, so the two cannot drift apart, and
// confirms each path actually materializes a package.
func TestEveryBuiltinImportPathResolves(t *testing.T) {
	for path := range BuiltinImportPaths {
		if _, ok := builtinPackageBuilders[path]; !ok {
			t.Errorf("BuiltinImportPaths has %q but builtinPackageBuilders does not", path)
		}
	}
	for path := range builtinPackageBuilders {
		if !BuiltinImportPaths[path] {
			t.Errorf("builtinPackageBuilders has %q but BuiltinImportPaths does not", path)
		}
	}

	paths := make([]string, 0, len(BuiltinImportPaths))
	for path := range BuiltinImportPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		// vm.Package is the route interp/loader's bindImports uses.
		vm := NewInterpreter()
		RegisterBuiltinPackages(vm)
		pkg, ok := vm.Package(path)
		if !ok {
			t.Errorf("vm.Package(%q) not resolved", path)
			continue
		}
		if pkg == nil {
			t.Errorf("vm.Package(%q) returned a nil package", path)
		}
	}
}

// TestBuiltinPackagesAreBuiltOnlyWhenNeeded is the point of the change: an
// interpreter that never touches a curated package must not have paid to
// construct it.
func TestBuiltinPackagesAreBuiltOnlyWhenNeeded(t *testing.T) {
	vm := NewInterpreter()
	RegisterBuiltinPackages(vm)
	if len(vm.packages) != 0 {
		t.Fatalf("RegisterBuiltinPackages eagerly built %d packages, want 0", len(vm.packages))
	}
	if _, ok := vm.Package("strings"); !ok {
		t.Fatal("strings did not resolve on demand")
	}
	if _, built := vm.packages["regexp"]; built {
		t.Error("regexp was built even though nothing asked for it")
	}
}

// TestImportBuildsPackageWithoutHostRegistration preserves the long-standing
// behavior that a plain Run/RunContext caller gets the curated packages from
// an import statement alone, without the host calling
// RegisterBuiltinPackages first.
func TestImportBuildsPackageWithoutHostRegistration(t *testing.T) {
	var out strings.Builder
	vm := NewInterpreter()
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			out.WriteString(ToString(args[0]))
		}
		return nil, nil
	})
	// Deliberately no RegisterBuiltinPackages call.
	err := vm.Run(`package main
import "strings"
func main() { ConsoleLog(strings.ToUpper("ok")) }`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "OK" {
		t.Errorf("output = %q, want %q", got, "OK")
	}
}

// TestCuratedPackageUsableWithoutImport pins a long-standing nanoGo
// convenience that lazy package construction could easily have removed:
// RegisterPackage declares each curated package into globals, so registering
// them all up front made `fmt.Println` resolve with no import statement at
// all. examples/quickstart is written that way. Building packages on first
// use has to keep it working, which is what packageForSelector does.
func TestCuratedPackageUsableWithoutImport(t *testing.T) {
	var out strings.Builder
	vm := NewInterpreter()
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			out.WriteString(ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		return ToString(args[0]), nil
	})
	RegisterBuiltinPackages(vm)

	// No import declarations anywhere in this program.
	if err := vm.Run(`package main
func main() {
	fmt.Println(strings.ToUpper("no import needed"))
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "NO IMPORT NEEDED" {
		t.Errorf("output = %q, want %q", got, "NO IMPORT NEEDED")
	}
}

// TestLocalBindingShadowsCuratedPackage checks the other half of
// packageForSelector: a real binding in scope wins over the curated package
// of the same name, so resolving a builtin lazily never reaches past a
// variable the program declared itself.
func TestLocalBindingShadowsCuratedPackage(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main
type sort struct { Field int }
func main() {
	sort := sort{Field: 3}
	_ = sort.Field
}`)
	if err != nil {
		t.Fatalf("a local binding should shadow the curated package: %v", err)
	}
}

// TestUnknownImportRemainsSilentNoOp keeps installImportedPackage's documented
// tolerance for an unrecognized path (see BuiltinImportPaths' comment).
func TestUnknownImportRemainsSilentNoOp(t *testing.T) {
	vm, _ := newTestVM()
	if err := vm.Run(`package main
import "not/a/real/package"
func main() {}`); err != nil {
		t.Fatalf("unknown import should be tolerated, got: %v", err)
	}
}

// TestEveryBuiltinPackageIsUsableFromGuestCode goes one level deeper than
// resolution: it calls something real in each curated package through guest
// source, so a builder that got mis-wired during extraction is caught rather
// than merely producing an empty package.
func TestEveryBuiltinPackageIsUsableFromGuestCode(t *testing.T) {
	cases := []struct {
		path string
		body string
		want string
	}{
		{"fmt", `ConsoleLog(fmt.Sprintf("%d", 7))`, "7"},
		{"strings", `ConsoleLog(strings.ToUpper("a"))`, "A"},
		{"strconv", `ConsoleLog(strconv.Itoa(12))`, "12"},
		{"math", `ConsoleLog(fmt2(math.Abs(-2.0)))`, "2"},
		{"sort", `a := []int{3, 1}; sort.Ints(a); ConsoleLog(strconv2(a[0]))`, "1"},
		{"path", `ConsoleLog(path.Base("/x/y.go"))`, "y.go"},
		{"unicode/utf8", `ConsoleLog(strconv2(utf8.RuneCountInString("ab")))`, "2"},
		{"encoding/json", `s, _ := json.Marshal(map[string]int{"k": 1}); ConsoleLog(s)`, `{"k":1}`},
		// nanoGo's regexp subset exposes MatchString/FindStringSubmatch only —
		// there is no FindString, and time carries no Duration constants
		// (Sleep and the timers take plain milliseconds). These snippets stay
		// inside the documented subset on purpose.
		{"regexp", `re, _ := regexp.Compile("a+"); ConsoleLog(strconv2(re.MatchString("baaa")))`, "true"},
		{"text/template", `s, _ := template.RenderString("hi", nil); ConsoleLog(s)`, "hi"},
		{"math/rand", `rand.Seed(1); ConsoleLog(strconv2(rand.Intn(1)))`, "0"},
		{"time", `ConsoleLog(strconv2(time.Now() > 0))`, "true"},
		{"os", `ConsoleLog(strconv2(len(os.Args) > 0))`, "true"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			alias := c.path
			if i := strings.LastIndex(alias, "/"); i >= 0 {
				alias = alias[i+1:]
			}
			var out strings.Builder
			vm := NewInterpreter()
			vm.Capabilities = FullCapabilities()
			vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
				if len(args) > 0 {
					out.WriteString(ToString(args[0]))
				}
				return nil, nil
			})
			vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
				if len(args) == 0 {
					return "", nil
				}
				return sprintfForTest(args), nil
			})
			// strconv2/fmt2 keep the guest snippets short without needing a
			// second import in every case.
			vm.RegisterNative("strconv2", func(args []any) (any, error) { return ToString(args[0]), nil })
			vm.RegisterNative("fmt2", func(args []any) (any, error) { return ToString(int(ToFloat(args[0]))), nil })
			RegisterBuiltinPackages(vm)

			src := "package main\nimport \"" + c.path + "\"\nfunc main() {\n" + c.body + "\n}\n"
			if err := vm.Run(src); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := out.String(); got != c.want {
				t.Errorf("%s: output = %q, want %q", alias, got, c.want)
			}
		})
	}
}

func sprintfForTest(args []any) string {
	format := ToString(args[0])
	rest := args[1:]
	out := format
	for _, a := range rest {
		for _, verb := range []string{"%d", "%s", "%v"} {
			if i := strings.Index(out, verb); i >= 0 {
				out = out[:i] + ToString(a) + out[i+2:]
				break
			}
		}
	}
	return out
}
