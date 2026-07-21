// function_test demonstrates interp/loader's data-driven test harness:
// grading an exported function against a list of input/expected-output
// cases without writing a *_test.go file at all — useful for exercise
// grading or iterative development. Run it with:
// go run ./examples/function_test
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
	vfs := vm.VFS

	write(vfs, "/pkg/go.mod", "module example.com/exercise\n")
	write(vfs, "/pkg/main.go", `package main

func Add(a, b int) int {
	return a + b
}

func main() {}
`)

	prog, err := loader.LoadModule(vfs, "/pkg", loader.Options{})
	if err != nil {
		log.Fatalf("LoadModule: %v", err)
	}

	results, err := loader.RunFunctionTest(context.Background(), vm, prog, "main.Add", []loader.TestCase{
		{Args: []any{2, 3}, Want: 5},
		{Args: []any{-1, 1}, Want: 0},
		{Args: []any{2, 3}, Want: 999}, // deliberately wrong, to show the classification
	})
	if err != nil {
		log.Fatalf("RunFunctionTest: %v", err)
	}

	for i, r := range results {
		fmt.Printf("case %d: %-11s got=%v want=%v\n", i, r.Category, r.Got, r.Want)
	}
}

func write(vfs *interp.VFS, filePath, src string) {
	dir := path.Dir(filePath)
	if err := vfs.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("MkdirAll %s: %v", dir, err)
	}
	if err := vfs.WriteFile(filePath, []byte(src), 0644); err != nil {
		log.Fatalf("WriteFile %s: %v", filePath, err)
	}
}
