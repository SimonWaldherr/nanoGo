// quickstart is the smallest possible nanoGo embedding: create an
// interpreter, register the standard packages and the two natives fmt
// needs, then run a guest program and check the error. Run it with:
// go run ./examples/quickstart
package main

import (
	"fmt"
	"log"
	"os"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)

	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprintln(os.Stdout, interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := interp.ToString(args[0])
		return fmt.Sprintf(format, args[1:]...), nil
	})

	if err := vm.Run(`package main
func main() {
	fmt.Println("hello from nanoGo!")
	fmt.Printf("6 * 7 = %d\n", 6*7)
}`); err != nil {
		log.Fatal(err)
	}
}
