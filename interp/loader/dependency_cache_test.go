package loader

import (
	"context"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestLoadModuleResolvesConfiguredDependencyRoot(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/app/go.mod", "module example.com/app\n")
	writeLoaderFile(t, vfs, "/app/main.go", `package main

import (
	"fmt"
	"example.com/shared/greet"
)

func main() { fmt.Println(greet.Message()) }
`)
	writeLoaderFile(t, vfs, "/deps/shared/go.mod", "module example.com/shared\n")
	writeLoaderFile(t, vfs, "/deps/shared/greet/greet.go", `package greet

func Message() string { return "configured dependency" }
`)

	prog, err := LoadModule(vfs, "/app", Options{DependencyRoots: map[string]string{
		"example.com/shared": "/deps/shared",
	}})
	if err != nil {
		t.Fatalf("LoadModule with dependency root: %v", err)
	}
	if _, ok := prog.Packages["/deps/shared/greet"]; !ok {
		t.Fatalf("resolved packages = %v, missing configured dependency", prog.Order)
	}
	vm, out := newLoaderTestVM()
	vm.VFS = vfs
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got, want := out.String(), "configured dependency\n"; got != want {
		t.Fatalf("dependency output = %q, want %q", got, want)
	}
}

func TestLoadModuleResolvesLocalGoModReplace(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/app/go.mod", `module example.com/app

replace example.com/shared => ./third_party/shared
`)
	writeLoaderFile(t, vfs, "/app/main.go", `package main

import (
	"fmt"
	"example.com/shared/greet"
)

func main() { fmt.Println(greet.Message()) }
`)
	writeLoaderFile(t, vfs, "/app/third_party/shared/go.mod", "module example.com/shared\n")
	writeLoaderFile(t, vfs, "/app/third_party/shared/greet/greet.go", `package greet

func Message() string { return "replaced dependency" }
`)

	prog, err := LoadModule(vfs, "/app", Options{})
	if err != nil {
		t.Fatalf("LoadModule with replace: %v", err)
	}
	vm, out := newLoaderTestVM()
	vm.VFS = vfs
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got, want := out.String(), "replaced dependency\n"; got != want {
		t.Fatalf("replace output = %q, want %q", got, want)
	}
}

func TestModuleCacheReusesParseAndInvalidatesAfterVFSWrite(t *testing.T) {
	vfs := interp.NewVFS()
	writeLoaderFile(t, vfs, "/cache/go.mod", "module example.com/cache\n")
	writeLoaderFile(t, vfs, "/cache/main.go", `package main

import "fmt"

func main() { fmt.Println("first") }
`)

	cache := NewModuleCache(vfs)
	first, err := cache.Load("/cache", Options{})
	if err != nil {
		t.Fatalf("first cache load: %v", err)
	}
	second, err := cache.Load("/cache", Options{})
	if err != nil {
		t.Fatalf("second cache load: %v", err)
	}
	if first == second {
		t.Fatal("cache returned the same Program; built state must not cross callers")
	}
	if first.Packages["/cache"].Files[0] != second.Packages["/cache"].Files[0] {
		t.Fatal("unchanged cache load re-parsed the source instead of reusing its immutable AST")
	}
	firstVM, firstOut := newLoaderTestVM()
	firstVM.VFS = vfs
	if err := RunProgram(context.Background(), firstVM, first, "main"); err != nil {
		t.Fatalf("RunProgram with first cached clone: %v", err)
	}
	secondVM, secondOut := newLoaderTestVM()
	secondVM.VFS = vfs
	if err := RunProgram(context.Background(), secondVM, second, "main"); err != nil {
		t.Fatalf("RunProgram with second cached clone: %v", err)
	}
	if firstOut.String() != "first\n" || secondOut.String() != "first\n" {
		t.Fatalf("cached clones must build independently, got %q and %q", firstOut.String(), secondOut.String())
	}

	writeLoaderFile(t, vfs, "/cache/main.go", `package main

import "fmt"

func main() { fmt.Println("second") }
`)
	third, err := cache.Load("/cache", Options{})
	if err != nil {
		t.Fatalf("load after VFS write: %v", err)
	}
	if third.Packages["/cache"].Files[0] == first.Packages["/cache"].Files[0] {
		t.Fatal("VFS write did not invalidate parsed module cache")
	}

	vm, out := newLoaderTestVM()
	vm.VFS = vfs
	if err := RunProgram(context.Background(), vm, third, "main"); err != nil {
		t.Fatalf("RunProgram after cache invalidation: %v", err)
	}
	if !strings.Contains(out.String(), "second") {
		t.Fatalf("reloaded program output = %q, want updated source", out.String())
	}
}
