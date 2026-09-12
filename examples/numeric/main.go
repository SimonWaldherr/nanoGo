// numeric demonstrates exact scalars, money, vectors and coordinates in nanoGo.
package main

import (
	_ "embed"
	"fmt"
	"log"

	"simonwaldherr.de/go/nanogo/interp"
)

//go:embed program.ng
var source string

func main() {
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})
	if err := vm.Run(source); err != nil {
		log.Fatal(err)
	}
}
