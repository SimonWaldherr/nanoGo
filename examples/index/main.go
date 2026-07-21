// index demonstrates interp/index's static, typeless analysis: scanning a
// small VFS-resident repo for every function/method, with a best-effort
// call graph (Calls/CalledBy) and simple AST-based complexity metrics. Run
// it with: go run ./examples/index
package main

import (
	"fmt"
	"log"
	"path"
	"sort"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/index"
)

func main() {
	vfs := interp.NewVFS()

	write(vfs, "/repo/main.go", `package main

import "example.com/repo/mathx"

func main() {
	Greet()
	mathx.Square(3)
}

// Greet prints a friendly hello.
func Greet() {
	println("hello")
}
`)
	write(vfs, "/repo/mathx/mathx.go", `package mathx

func Square(n int) int {
	return n * n
}
`)

	entries, err := index.Scan(vfs, "/repo", index.Options{})
	if err != nil {
		log.Fatalf("Scan: %v", err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for _, e := range entries {
		fmt.Printf("%-20s calls=%v calledBy=%v complexity=%d loc=%d\n",
			e.ID, e.Calls, e.CalledBy, e.Metrics.CyclomaticComplexity, e.Metrics.LOC)
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
