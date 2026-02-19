package main

import (
	"strings"
	"testing"
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
	imports := []string{`import "fmt"`}
	src := buildDeclSource(imports, "func greet() {}")
	if !strings.Contains(src, "package main") {
		t.Error("missing package main")
	}
	if !strings.Contains(src, `import "fmt"`) {
		t.Error("missing import")
	}
	if !strings.Contains(src, "func greet()") {
		t.Error("missing declaration")
	}
	if !strings.Contains(src, "func main() {}") {
		t.Error("missing empty main")
	}
}

func TestBuildStmtSource(t *testing.T) {
	imports := []string{`import "fmt"`}
	src := buildStmtSource(imports, `fmt.Println("hello")`)
	if !strings.Contains(src, "package main") {
		t.Error("missing package main")
	}
	if !strings.Contains(src, `import "fmt"`) {
		t.Error("missing import")
	}
	if !strings.Contains(src, "func main()") {
		t.Error("missing main function")
	}
	if !strings.Contains(src, `fmt.Println("hello")`) {
		t.Error("missing statement")
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
