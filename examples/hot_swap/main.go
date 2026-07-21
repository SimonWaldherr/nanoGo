// hot_swap demonstrates loader.ReplaceFunction. Run it with:
// go run ./examples/hot_swap
package main

import (
	"context"
	"fmt"
	"log"
	"path"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
)

func main() {
	vm := interp.NewInterpreter()
	write(vm.VFS, "/app/go.mod", "module example.com/app\n")
	write(vm.VFS, "/app/main.go", `package main

func Add(a, b int) int { return a + b }
`)

	prog, err := loader.LoadModule(vm.VFS, "/app", loader.Options{})
	if err != nil {
		log.Fatalf("LoadModule: %v", err)
	}

	ctx := context.Background()
	showResult(ctx, vm, prog, "before hot-swap")
	if err := loader.ReplaceFunction(vm, prog, "main", "Add", `
func Add(a, b int) int { return a - b }
`); err != nil {
		log.Fatalf("ReplaceFunction: %v", err)
	}
	showResult(ctx, vm, prog, "after hot-swap")
}

func showResult(ctx context.Context, vm *interp.Interpreter, prog *loader.Program, label string) {
	results, err := loader.RunFunctionTest(ctx, vm, prog, "main.Add", []loader.TestCase{
		{Args: []any{7, 2}, Want: 5},
	})
	if err != nil {
		log.Fatalf("RunFunctionTest: %v", err)
	}
	result := results[0]
	fmt.Printf("%s: %s (got %v, want %v)\n", label, result.Category, result.Got, result.Want)
}

func write(vfs *interp.VFS, filePath, src string) {
	if err := vfs.MkdirAll(path.Dir(filePath), 0755); err != nil {
		log.Fatalf("MkdirAll %s: %v", filePath, err)
	}
	if err := vfs.WriteFile(filePath, []byte(src), 0644); err != nil {
		log.Fatalf("WriteFile %s: %v", filePath, err)
	}
}
