// vfs_mount demonstrates an embedded read-only filesystem plus a bounded
// reader snapshot. Run it with: go run ./examples/vfs_mount
package main

import (
	"context"
	"embed"
	"fmt"
	iofs "io/fs"
	"log"
	"strings"

	"simonwaldherr.de/go/nanogo/interp"
)

//go:embed assets/*
var embeddedAssets embed.FS

func main() {
	vfs := interp.NewVFS()
	assets, err := iofs.Sub(embeddedAssets, "assets")
	if err != nil {
		log.Fatal(err)
	}
	if err := vfs.MountFS("/assets", assets); err != nil {
		log.Fatal(err)
	}
	if err := vfs.ImportReader(context.Background(), "/input/request.txt", strings.NewReader("request snapshot"), 1024); err != nil {
		log.Fatal(err)
	}

	vm := interp.NewInterpreterWithVFS(vfs)
	vm.Capabilities = interp.Capabilities{FileSystem: interp.FileSystemCapabilities{
		Read:      true,
		ReadPaths: []string{"/assets", "/input"},
	}}
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

	err = vm.RunContext(context.Background(), `package main
import (
	"fmt"
	"os"
)
func main() {
	asset, _ := os.ReadFile("/assets/hello.txt")
	input, _ := os.ReadFile("/input/request.txt")
	fmt.Println(asset)
	fmt.Println(input)
}`)
	if err != nil {
		log.Fatal(err)
	}
}
