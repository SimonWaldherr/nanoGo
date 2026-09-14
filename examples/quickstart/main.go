// quickstart is the smallest possible nanoGo embedding: create an
// interpreter, enable the curated packages and connect console output,
// then run a guest program and check the error. Run it with:
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
	if err := vm.Run(`package main
import "fmt"
func main() {
	fmt.Println("hello from nanoGo!")
	fmt.Println(fmt.Sprintf("6 * 7 = %d", 6*7))
}`); err != nil {
		log.Fatal(err)
	}
}
