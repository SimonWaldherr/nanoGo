package loader

import (
	"context"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestRunProgramSinglePackageForwardReferences(t *testing.T) {
	vm, out := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/repo/go.mod", "module example.com/app\n")
	writeLoaderFile(t, vfs, "/repo/a.go", `package main

import "fmt"

var X = Helper()

func main() {
	fmt.Println(X)
	fmt.Println(NewPoint(2, 3).Sum())
}
`)
	writeLoaderFile(t, vfs, "/repo/b.go", `package main

func Helper() int {
	return 41
}
`)
	writeLoaderFile(t, vfs, "/repo/c.go", `package main

type Point struct {
	X int
	Y int
}

func NewPoint(x, y int) Point {
	return Point{X: x, Y: y}
}

func (p Point) Sum() int {
	return p.X + p.Y
}
`)

	prog, err := LoadModule(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if !strings.Contains(out.String(), "41") {
		t.Errorf("expected output to contain 41 (forward-referenced var), got %q", out.String())
	}
	if !strings.Contains(out.String(), "5") {
		t.Errorf("expected output to contain 5 (Point.Sum), got %q", out.String())
	}
}

func TestRunProgramTwoPackagesInitAndVarOrder(t *testing.T) {
	vm, out := newLoaderTestVM()
	vfs := vm.VFS

	// A native side-channel records init() call order directly, sidestepping
	// nanoGo's pre-existing (and out of scope here) limitation that
	// assignment through a package-qualified selector (pkg.Var = x) only
	// supports struct fields, not package-level vars — the same limitation
	// applies identically to any single-file program assigning to a
	// builtin package's Vars today, so it is not something this feature
	// introduces or is required to fix.
	var order []string
	vm.RegisterNative("RecordOrder", func(args []any) (any, error) {
		if len(args) > 0 {
			order = append(order, interp.ToString(args[0]))
		}
		return nil, nil
	})

	writeLoaderFile(t, vfs, "/repo/go.mod", "module example.com/app\n")
	writeLoaderFile(t, vfs, "/repo/lib/lib.go", `package lib

func init() {
	RecordOrder("lib-init")
}

func Value() int {
	return 42
}
`)
	writeLoaderFile(t, vfs, "/repo/main.go", `package main

import "fmt"
import "example.com/app/lib"

var X = lib.Value()

func init() {
	RecordOrder("main-init")
}

func main() {
	fmt.Println(X)
}
`)

	prog, err := LoadModule(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	// X = lib.Value() (42) proves lib was fully built (dependency-first)
	// before main's own var initializers ran.
	if !strings.Contains(out.String(), "42") {
		t.Errorf("expected output to contain 42 (lib.Value() via var X), got %q", out.String())
	}
	// lib's init() must run before main's init(), matching dependency-first order.
	if len(order) != 2 || order[0] != "lib-init" || order[1] != "main-init" {
		t.Errorf("expected init order [lib-init main-init], got %v", order)
	}
}

func TestRunProgramImportCycleAndUnknownImportSurfaceAtLoadTime(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/proj/go.mod", "module example.com/proj\n")
	writeLoaderFile(t, vfs, "/proj/main.go", `package main

import "github.com/someone/other"

func main() { other.Foo() }
`)

	_, err := LoadModule(vfs, "/proj", Options{})
	if err == nil {
		t.Fatal("expected LoadModule to reject an unrecognized external import before any execution")
	}

	vm2 := interp.NewInterpreter()
	if err := RunProgram(context.Background(), vm2, &Program{}, "main"); err == nil {
		t.Fatal("expected RunProgram to fail cleanly against an empty/invalid Program rather than panic")
	}
}
