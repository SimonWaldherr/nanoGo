// capabilities demonstrates the deny-by-default policy for nanoGo's curated
// filesystem and HTTP packages. Run it with: go run ./examples/capabilities
package main

import (
	"context"
	"fmt"
	"log"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	vm := interp.NewInterpreter()
	vm.Capabilities = interp.Capabilities{
		FileSystem: interp.FileSystemCapabilities{Read: true, Write: true},
		Network: interp.NetworkCapabilities{
			HTTP:         true,
			AllowedHosts: []string{"api.example.com"},
		},
	}
	interp.RegisterBuiltinPackages(vm)

	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		return fmt.Sprint(args...), nil
	})
	// The interpreter validates the URL against AllowedHosts before this native
	// is reached. A real host would make the context-aware HTTP request here.
	vm.RegisterNativeContext("HTTPGetText", func(ctx context.Context, args []any) (any, error) {
		return "safe host request to " + interp.ToString(args[0]), nil
	})

	err := vm.RunContext(context.Background(), `package main
import (
	"fmt"
	"http"
	"os"
)

func main() {
	os.WriteFile("/tmp/message.txt", "stored in the VFS", 0644)
	message, _ := os.ReadFile("/tmp/message.txt")
	fmt.Println(message)
	fmt.Println(http.GetText("https://api.example.com/status"))
}`)
	if err != nil {
		log.Fatal(err)
	}
}
