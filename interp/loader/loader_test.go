package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestLoadModuleTwoPackagesOrder(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/repo/go.mod", "module example.com/app\n\ngo 1.21\n")
	writeLoaderFile(t, vfs, "/repo/main.go", `package main

import "example.com/app/lib"

func main() {
	lib.Hello()
}
`)
	writeLoaderFile(t, vfs, "/repo/lib/lib.go", `package lib

func Hello() {}
`)

	prog, err := LoadModule(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if prog.ModulePath != "example.com/app" {
		t.Errorf("ModulePath = %q, want example.com/app", prog.ModulePath)
	}
	if len(prog.Packages) != 2 {
		t.Fatalf("expected 2 packages, got %d: %v", len(prog.Packages), prog.Order)
	}
	if _, ok := prog.Packages["/repo"]; !ok {
		t.Errorf("missing entry package /repo")
	}
	if _, ok := prog.Packages["/repo/lib"]; !ok {
		t.Errorf("missing dependency package /repo/lib")
	}

	// Dependency-first: /repo/lib must appear before /repo in Order.
	libIdx, mainIdx := -1, -1
	for i, d := range prog.Order {
		switch d {
		case "/repo/lib":
			libIdx = i
		case "/repo":
			mainIdx = i
		}
	}
	if libIdx == -1 || mainIdx == -1 {
		t.Fatalf("Order missing expected dirs: %v", prog.Order)
	}
	if libIdx > mainIdx {
		t.Errorf("expected /repo/lib before /repo in Order, got %v", prog.Order)
	}
}

func TestLoadModuleImportCycleErrorsWithoutHanging(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/cyc/go.mod", "module example.com/cyc\n")
	writeLoaderFile(t, vfs, "/cyc/a/a.go", `package a

import "example.com/cyc/b"

func F() { b.G() }
`)
	writeLoaderFile(t, vfs, "/cyc/b/b.go", `package b

import "example.com/cyc/a"

func G() { a.F() }
`)

	done := make(chan error, 1)
	go func() {
		_, err := LoadModule(vfs, "/cyc", Options{EntryDir: "/cyc/a"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected an import cycle error, got nil")
		}
		if !strings.Contains(err.Error(), "import cycle") {
			t.Errorf("expected an import cycle error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadModule hung instead of returning a cycle error")
	}
}

func TestLoadModuleUnknownImportErrors(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/proj/go.mod", "module example.com/proj\n")
	writeLoaderFile(t, vfs, "/proj/main.go", `package main

import "github.com/someone/other"

func main() {}
`)

	_, err := LoadModule(vfs, "/proj", Options{})
	if err == nil {
		t.Fatal("expected an error for an unrecognized external import")
	}
	if !strings.Contains(err.Error(), "github.com/someone/other") {
		t.Errorf("expected the error to name the offending import, got: %v", err)
	}
}

func TestLoadModuleRecognizesPathAndUTF8Builtins(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/proj/go.mod", "module example.com/proj\n")
	writeLoaderFile(t, vfs, "/proj/main.go", `package main

import (
    "fmt"
    "path"
    "unicode/utf8"
)

func main() {
    fmt.Println(path.Base("/api/users.json"), utf8.RuneCountInString("Go✓"))
}

`)

	prog, err := LoadModule(vfs, "/proj", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	vm, out := newLoaderTestVM()
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got, want := out.String(), "users.json 3\n"; got != want {
		t.Fatalf("builtin package output = %q, want %q", got, want)
	}
}

func TestLoadModuleDiscoversTestOnlyLocalImports(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/proj/go.mod", "module example.com/proj\n")
	writeLoaderFile(t, vfs, "/proj/main.go", `package main

func main() {}
`)
	writeLoaderFile(t, vfs, "/proj/helper/helper.go", `package helper

func Value() int { return 42 }
`)
	writeLoaderFile(t, vfs, "/proj/main_test.go", `package main

import (
	"testing"
	"example.com/proj/helper"
)

func TestHelper(t *testing.T) {
	if helper.Value() != 42 { t.Fatalf("helper import failed") }
}

`)

	prog, err := LoadModule(vfs, "/proj", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if _, ok := prog.Packages["/proj/helper"]; !ok {
		t.Fatalf("test-only dependency missing from %v", prog.Order)
	}
	vm, _ := newLoaderTestVM()
	vm.VFS = vfs
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if _, built := prog.built["/proj/helper"]; built {
		t.Fatal("test-only helper was initialized during production RunProgram")
	}
	results, err := RunPackageTests(context.Background(), vm, prog, "main")
	if err != nil {
		t.Fatalf("RunPackageTests: %v", err)
	}
	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("test-only import results = %+v", results)
	}
	if _, built := prog.built["/proj/helper"]; !built {
		t.Fatal("test-only helper was not initialized for RunPackageTests")
	}
}

func TestLoadModuleRunsInternalAndExternalTestsWithSeparateInitialization(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/calc/go.mod", "module example.com/calc\n")
	writeLoaderFile(t, vfs, "/calc/calc.go", `package calc

var testInitLeak = 0

func Double(n int) int { return n * 2 }

func InitLeakValue() int { return testInitLeak }
`)
	writeLoaderFile(t, vfs, "/calc/calc_test.go", `package calc

import "testing"

var internalReady = 1

func init() { internalReady = 2; testInitLeak = 2 }

func TestInternal(t *testing.T) {
	if internalReady != 2 { t.Error("internal test init was not run") }
}
`)
	writeLoaderFile(t, vfs, "/calc/external_test.go", `package calc_test

import (
	"testing"
	"example.com/calc"
)

var externalReady = 1

func init() { externalReady = 2 }

func TestPublicAPI(t *testing.T) {
	if calc.Double(3) != 6 { t.Error("unexpected public result") }
	if externalReady != 2 { t.Error("external test init was not run") }
}

func BenchmarkPublicAPI(b *testing.B) {
	for i := 0; i < b.N; i++ { calc.Double(i) }
}
`)

	prog, err := LoadModule(vfs, "/calc", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	pp := prog.Packages["/calc"]
	if pp == nil || len(pp.TestFiles) != 1 || len(pp.ExternalTestFiles) != 1 || pp.ExternalTestName != "calc_test" {
		t.Fatalf("test package split = %+v", pp)
	}

	vm, _ := newLoaderTestVM()
	vm.VFS = vfs
	before, err := RunFunctionTest(context.Background(), vm, prog, "calc.InitLeakValue", []TestCase{{Want: 0}})
	if err != nil || len(before) != 1 || !before[0].Pass {
		t.Fatalf("test-only init leaked into production build: results=%+v err=%v", before, err)
	}
	results, err := RunPackageTests(context.Background(), vm, prog, "calc")
	if err != nil {
		t.Fatalf("RunPackageTests: %v", err)
	}
	if len(results) != 2 || !results[0].Pass || !results[1].Pass || results[0].Name != "TestInternal" || results[1].Name != "TestPublicAPI" {
		t.Fatalf("test results = %+v, want internal and external pass", results)
	}
	benches, err := RunPackageBenchmarks(context.Background(), vm, prog, "calc", BenchOptions{MinIterations: 3})
	if err != nil || len(benches) != 1 || benches[0].Iterations != 3 {
		t.Fatalf("external benchmark results = %+v, err=%v", benches, err)
	}
}
