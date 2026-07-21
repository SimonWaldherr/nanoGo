// multi_package demonstrates loading a small multi-file, multi-package
// program from the VFS with interp/loader: a go.mod module path resolves a
// local import, dependency-first init()/var order is honored, and RunProgram
// then calls main() exactly like RunContext would for a single file. Run it
// with: go run ./examples/multi_package
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
	vm.Capabilities = interp.FullCapabilities()
	registerConsole(vm)
	interp.RegisterBuiltinPackages(vm)

	vfs := vm.VFS
	write(vfs, "/app/go.mod", "module example.com/app\n")
	write(vfs, "/app/greeting/greeting.go", `package greeting

func For(name string) string {
	return "Hello, " + name + "!"
}
`)
	write(vfs, "/app/main.go", `package main

import (
	"fmt"
	"example.com/app/greeting"
)

func main() {
	fmt.Println(greeting.For("nanoGo"))
}
`)

	prog, err := loader.LoadModule(vfs, "/app", loader.Options{})
	if err != nil {
		log.Fatalf("LoadModule: %v", err)
	}
	if err := loader.RunProgram(context.Background(), vm, prog, "main"); err != nil {
		log.Fatalf("RunProgram: %v", err)
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

func registerConsole(vm *interp.Interpreter) {
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return fmt.Sprintf(interp.ToString(args[0]), args[1:]...), nil
	})
}
