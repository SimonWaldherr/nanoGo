package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestLooksLikeDecl(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"func greet() {}", true},
		{"type Foo struct{}", true},
		{"const Pi = 3", true},
		{"var x = 5", true},
		{"fmt.Println(x)", false},
		{"x := 5", false},
		{"import \"fmt\"", false}, // handled separately from declarations
	}
	for _, c := range cases {
		got := looksLikeDecl(c.in)
		if got != c.want {
			t.Errorf("looksLikeDecl(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTryConvertShortVarDecl(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"x := 5", "var x = 5", true},
		{"name := \"alice\"", "var name = \"alice\"", true},
		{"_x := 10", "var _x = 10", true},
		{"x, y := 1, 2", "", false},   // multi-value: not converted (safety)
		{"fmt.Println(x)", "", false}, // no :=
		{"x := ", "var x = ", true},   // edge case: empty rhs
	}
	for _, c := range cases {
		got, ok := tryConvertShortVarDecl(c.in)
		if ok != c.ok {
			t.Errorf("tryConvertShortVarDecl(%q): ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("tryConvertShortVarDecl(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildDeclSource(t *testing.T) {
	src := buildDeclSource("func greet() {}")
	if !strings.Contains(src, "package main") {
		t.Error("missing package main")
	}
	if strings.Contains(src, "import ") {
		t.Error("declaration source must not repeat prior imports")
	}
	if !strings.Contains(src, "func greet()") {
		t.Error("missing declaration")
	}
	if !strings.Contains(src, "func main() {}") {
		t.Error("missing empty main")
	}
}

func TestBuildStmtSource(t *testing.T) {
	src := buildStmtSource(`fmt.Println("hello")`)
	if !strings.Contains(src, "package main") {
		t.Error("missing package main")
	}
	if strings.Contains(src, "import ") {
		t.Error("statement source must not repeat prior imports")
	}
	if !strings.Contains(src, "func main()") {
		t.Error("missing main function")
	}
	if !strings.Contains(src, `fmt.Println("hello")`) {
		t.Error("missing statement")
	}
}

func TestImportedAliasPersistsWithoutRepeatedImports(t *testing.T) {
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)
	if err := runREPLSource(vm, time.Second, "package main\nimport f \"fmt\"\nfunc main() {}\n"); err != nil {
		t.Fatalf("import alias: %v", err)
	}
	if err := runREPLSource(vm, time.Second, buildStmtSource(`f.Println("ok")`)); err != nil {
		t.Fatalf("use persisted alias: %v", err)
	}
}

func TestREPLSourceTimeout(t *testing.T) {
	vm := interp.NewInterpreter()
	err := runREPLSource(vm, 20*time.Millisecond, "package main\nfunc main() { for { } }\n")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want deadline exceeded", err)
	}
}

func TestTimeoutDescription(t *testing.T) {
	if got := timeoutDescription(0); got != "off" {
		t.Fatalf("disabled timeout = %q", got)
	}
	if got := timeoutDescription(2 * time.Second); got != "2s" {
		t.Fatalf("enabled timeout = %q", got)
	}
}

func TestIsSimpleIdent(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"x", true},
		{"myVar", true},
		{"_x", true},
		{"x1", true},
		{"", false},
		{"1x", false},
		{"x.y", false},
		{"x y", false},
	}
	for _, c := range cases {
		got := isSimpleIdent(c.in)
		if got != c.want {
			t.Errorf("isSimpleIdent(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
