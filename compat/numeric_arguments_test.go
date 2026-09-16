package compat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
)

func TestNumericArgumentsRequireExplicitConversions(t *testing.T) {
	programs := []string{
		`func f(x float64){};func main(){n:=1;f(n)}`,
		`const n int=1;func f(x float64){};func main(){f(n)}`,
		`func f(x int){};func main(){n:=1.0;f(n)}`,
		`func f(x int){};func main(){f(1.5)}`,
		`func f(x int8){};func main(){f(128)}`,
		`func f(x int8){};func main(){n:=1;f(n)}`,
		`func f(x int){};func main(){var n int8=1;f(n)}`,
		`type Count int;func f(x Count){};func main(){n:=1;f(n)}`,
		`type Small int8;func f(x Small){};func main(){f(128)}`,
		`type Small int8;func f(x Small){};func main(){f(-129)}`,
		`type Octet uint8;func f(x Octet){};func main(){f(-1)}`,
		`type Octet uint8;func f(x Octet){};func main(){f(256)}`,
		`type Small int8;type Derived Small;func f(x Derived){};func main(){f(128)}`,
		`type Single float32;func f(x Single){};func main(){f(3.4028236e38)}`,
		`type Flag bool;func f(x Flag){};func main(){f(1)}`,
		`type Text string;func f(x Text){};func main(){f(65)}`,
	}
	for _, body := range programs {
		src := "package main\n" + body
		t.Run(body, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "main.go")
			if err := os.WriteFile(filename, []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("go", "run", filename).CombinedOutput(); err == nil {
				t.Fatalf("invalid oracle program accepted: %s", output)
			}
			vm := interp.NewInterpreter()
			if err := vm.Run(src); err == nil {
				t.Fatal("direct interpreter accepted invalid argument")
			}
			vm = interp.NewInterpreter()
			_ = vm.VFS.MkdirAll("/app", 0755)
			_ = vm.VFS.WriteFile("/app/main.go", []byte(src), 0600)
			program, err := loader.LoadModule(vm.VFS, "/app", loader.Options{ModulePath: "arguments.local/app"})
			if err != nil {
				t.Fatal(err)
			}
			if err := loader.RunProgram(context.Background(), vm, program, "main"); err == nil {
				t.Fatal("loader accepted invalid argument")
			}
		})
	}
}
