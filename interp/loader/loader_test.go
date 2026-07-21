package loader

import (
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
