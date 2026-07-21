// benchmark demonstrates interp/loader's deterministic benchmark runner:
// StepsPerOp (evaluator checkpoints, not CPU time) is identical across
// repeated runs of the same function, unlike wall-clock timing. Run it with:
// go run ./examples/benchmark
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

	write(vfs, "/pkg/go.mod", "module example.com/bench\n")
	write(vfs, "/pkg/main.go", `package main

func SumTo(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}

func main() {}
`)

	prog, err := loader.LoadModule(vfs, "/pkg", loader.Options{})
	if err != nil {
		log.Fatalf("LoadModule: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, err := loader.RunFunctionBench(context.Background(), vm, prog, "main.SumTo", loader.BenchOptions{MinIterations: 2000})
		if err != nil {
			log.Fatalf("RunFunctionBench: %v", err)
		}
		fmt.Printf("run %d: %d steps/op, %.0f ns/op (wall, informational), %d iterations\n",
			i, result.StepsPerOp, result.WallNsPerOp, result.Iterations)
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
