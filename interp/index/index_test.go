package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

func writeFile(t *testing.T, vfs *interp.VFS, p, src string) {
	t.Helper()
	dir := p[:strings.LastIndex(p, "/")]
	if err := vfs.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := vfs.WriteFile(p, []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}

func TestScanCallGraphAndMetrics(t *testing.T) {
	vfs := interp.NewVFS()
	writeFile(t, vfs, "/repo/main.go", `package main

import "example.com/app/mathx"

func main() {
	Greet()
	mathx.Square(3)
}

// Greet says hello.
func Greet() {
	if true {
		for i := 0; i < 1; i++ {
			println("hi")
		}
	}
}
`)
	writeFile(t, vfs, "/repo/mathx/mathx.go", `package mathx

func Square(n int) int {
	return n * n
}
`)
	writeFile(t, vfs, "/repo/main_test.go", `package main

func TestGreet(t *T) {
	Greet()
}
`)

	entries, err := Scan(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	byID := map[string]FunctionEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}

	main, ok := byID["main.main"]
	if !ok {
		t.Fatalf("main.main not found among entries: %+v", entries)
	}
	if !containsStr(main.Calls, "main.Greet") {
		t.Errorf("expected main.main to call main.Greet, got Calls=%v", main.Calls)
	}
	if !containsStr(main.Calls, "mathx.Square") {
		t.Errorf("expected main.main to call mathx.Square via the mathx qualifier, got Calls=%v", main.Calls)
	}

	greet, ok := byID["main.Greet"]
	if !ok {
		t.Fatalf("main.Greet not found")
	}
	if !containsStr(greet.CalledBy, "main.main") {
		t.Errorf("expected main.Greet.CalledBy to include main.main, got %v", greet.CalledBy)
	}
	if greet.Doc != "Greet says hello." {
		t.Errorf("Greet.Doc = %q, want %q", greet.Doc, "Greet says hello.")
	}
	if !containsStr(greet.Tests, "TestGreet") {
		t.Errorf("expected main.Greet.Tests to include TestGreet, got %v", greet.Tests)
	}
	// if + for => cyclomatic complexity 1 (base) + 2 = 3; nested if->for->block => depth 3.
	if greet.Metrics.CyclomaticComplexity != 3 {
		t.Errorf("Greet CyclomaticComplexity = %d, want 3", greet.Metrics.CyclomaticComplexity)
	}
	if greet.Metrics.MaxNestingDepth < 3 {
		t.Errorf("Greet MaxNestingDepth = %d, want >= 3", greet.Metrics.MaxNestingDepth)
	}

	square, ok := byID["mathx.Square"]
	if !ok {
		t.Fatalf("mathx.Square not found")
	}
	if !containsStr(square.CalledBy, "main.main") {
		t.Errorf("expected mathx.Square.CalledBy to include main.main, got %v", square.CalledBy)
	}
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// parseTestFunc parses src (a full "package p" source with exactly one
// top-level function declaration) and returns that *ast.FuncDecl, for
// feeding directly into computeMetrics without going through Scan/VFS.
func parseTestFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "metrics_test_input.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			return fd
		}
	}
	t.Fatalf("no func decl found in source:\n%s", src)
	return nil
}

// TestComputeMetricsCyclomaticComplexity exercises computeMetrics' switch
// cases that TestScanCallGraphAndMetrics never reaches: CaseClause (in a
// switch), CommClause (in a select), and BinaryExpr with && / ||. In
// particular it pins the documented "a bare default: doesn't add a branch"
// contract for both switch and select.
func TestComputeMetricsCyclomaticComplexity(t *testing.T) {
	tests := []struct {
		name           string
		src            string
		wantComplexity int
	}{
		{
			name: "switch with three non-default cases and a bare default",
			src: `package p

func F(x int) int {
	switch x {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		return 0
	}
}
`,
			// base 1 + one per non-default case (3); the bare default must
			// not add a fourth branch.
			wantComplexity: 4,
		},
		{
			name: "select with two comm clauses and a bare default",
			src: `package p

func F(a, b chan int) {
	select {
	case <-a:
	case <-b:
	default:
	}
}
`,
			// base 1 + one per non-default comm clause (2); the bare
			// default must not add a third branch.
			wantComplexity: 3,
		},
		{
			name: "if condition combining && and ||",
			src: `package p

func F(a, b, c bool) {
	if a && b || c {
		println("x")
	}
}
`,
			// base 1 + IfStmt(1) + "&&"(1) + "||"(1): both binary operators
			// in "a && b || c" must be counted, not just one of them.
			wantComplexity: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := parseTestFunc(t, tt.src)
			m := computeMetrics(fd)
			if m.CyclomaticComplexity != tt.wantComplexity {
				t.Errorf("CyclomaticComplexity = %d, want %d", m.CyclomaticComplexity, tt.wantComplexity)
			}
		})
	}
}

// TestComputeMetricsNestingDepth exercises blockDepth's handling of
// SwitchStmt (via caseClauseDepth) nested inside a for nested inside an if,
// the one control-flow shape metrics.go supports that
// TestScanCallGraphAndMetrics never builds.
func TestComputeMetricsNestingDepth(t *testing.T) {
	const baselineSrc = `package p

func F(a, b bool) {
	if a {
		if b {
			println("x")
		}
	}
}
`
	const switchSrc = `package p

func F(a bool, n, x int) {
	if a {
		for i := 0; i < n; i++ {
			switch x {
			case 1:
				println("x")
			}
		}
	}
}
`

	t.Run("baseline: if inside if", func(t *testing.T) {
		fd := parseTestFunc(t, baselineSrc)
		m := computeMetrics(fd)
		// function block(1) -> outer if's block(2) -> inner if's block(3).
		if m.MaxNestingDepth != 3 {
			t.Errorf("MaxNestingDepth = %d, want 3", m.MaxNestingDepth)
		}
	})

	t.Run("switch inside for inside if", func(t *testing.T) {
		fd := parseTestFunc(t, switchSrc)
		m := computeMetrics(fd)
		// By analogy with the "if inside if" baseline above (each additional
		// block-containing statement adds exactly one level of depth),
		// replacing the innermost "if" with a "switch" at the same syntactic
		// position -- if -> for -> switch/case -> stmt, still four
		// block-containing levels deep -- reports MaxNestingDepth 4
		// (function block, if-block, for-block, switch's case-block):
		// blockDepth's SwitchStmt/TypeSwitchStmt/SelectStmt cases now add a
		// level before recursing into case bodies, matching how
		// IfStmt/ForStmt/RangeStmt route their body through the BlockStmt
		// case's always-add-one-level behavior.
		if m.MaxNestingDepth != 4 {
			t.Errorf("MaxNestingDepth = %d, want 4", m.MaxNestingDepth)
		}
	})
}

// TestScanMethodCallResolution exercises buildCallGraph's third resolution
// branch -- `default: targets = byMethodName[ref.Name]`, used for X.Sel
// calls where X is a receiver-typed variable rather than an empty qualifier
// or a known package name -- which TestScanCallGraphAndMetrics never
// reaches (it only calls a bare function and a package-qualified one). It
// also pins the FunctionEntry.ID format for methods, "pkg.(Receiver).Name".
func TestScanMethodCallResolution(t *testing.T) {
	vfs := interp.NewVFS()
	writeFile(t, vfs, "/repo/main.go", `package pkg

type T struct{}

// Do does something.
func (t T) Do() {
	println("do")
}

func Use(t T) {
	t.Do()
}
`)

	entries, err := Scan(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	byID := map[string]FunctionEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}

	do, ok := byID["pkg.(T).Do"]
	if !ok {
		t.Fatalf(`method entry "pkg.(T).Do" not found among entries: %+v`, entries)
	}
	if do.Receiver != "T" {
		t.Errorf("Do.Receiver = %q, want %q", do.Receiver, "T")
	}
	if !containsStr(do.CalledBy, "pkg.Use") {
		t.Errorf("expected pkg.(T).Do.CalledBy to include pkg.Use (via byMethodName), got %v", do.CalledBy)
	}

	use, ok := byID["pkg.Use"]
	if !ok {
		t.Fatalf("pkg.Use not found among entries: %+v", entries)
	}
	if !containsStr(use.Calls, "pkg.(T).Do") {
		t.Errorf("expected pkg.Use.Calls to include pkg.(T).Do, got %v", use.Calls)
	}
}

// TestScanAmbiguousMethodNameAcrossReceivers pins the current, documented
// behavior of buildCallGraph's byMethodName resolution when two distinct
// receiver types define a same-named method: since byMethodName is keyed
// only by method name (not by receiver type), a call through a variable of
// either type resolves against *both* methods -- the ambiguity the
// buildCallGraph doc comment calls out ("ambiguous when multiple types
// share a method name -- not disambiguated here"). This is a baseline test
// for that known limitation, not an assertion that the over-linking is
// desirable; a future disambiguation fix should update these expectations.
func TestScanAmbiguousMethodNameAcrossReceivers(t *testing.T) {
	vfs := interp.NewVFS()
	writeFile(t, vfs, "/repo/main.go", `package pkg

type T struct{}
type U struct{}

func (t T) Do() {
	println("T.Do")
}

func (u U) Do() {
	println("U.Do")
}

func UseT(t T) {
	t.Do()
}

func UseU(u U) {
	u.Do()
}
`)

	entries, err := Scan(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	byID := map[string]FunctionEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}

	tDo, ok := byID["pkg.(T).Do"]
	if !ok {
		t.Fatalf("pkg.(T).Do not found among entries: %+v", entries)
	}
	uDo, ok := byID["pkg.(U).Do"]
	if !ok {
		t.Fatalf("pkg.(U).Do not found among entries: %+v", entries)
	}

	// Current (ambiguous, non-disambiguated) behavior: both UseT and UseU
	// resolve to *both* Do methods, because byMethodName["Do"] contains
	// both entries regardless of the receiver-variable's actual type.
	for _, callee := range []struct {
		name  string
		entry FunctionEntry
	}{
		{"pkg.(T).Do", tDo},
		{"pkg.(U).Do", uDo},
	} {
		if !containsStr(callee.entry.CalledBy, "pkg.UseT") {
			t.Errorf("expected %s.CalledBy to include pkg.UseT (ambiguous byMethodName match), got %v", callee.name, callee.entry.CalledBy)
		}
		if !containsStr(callee.entry.CalledBy, "pkg.UseU") {
			t.Errorf("expected %s.CalledBy to include pkg.UseU (ambiguous byMethodName match), got %v", callee.name, callee.entry.CalledBy)
		}
	}
}
