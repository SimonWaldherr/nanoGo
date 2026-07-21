package loader

import (
	"fmt"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

// newLoaderTestVM builds an *interp.Interpreter configured the same way
// interp's own internal test helper does (see interp_test.go's newTestVM),
// since interp/loader tests are an external package and can't reuse that
// unexported helper directly.
func newLoaderTestVM() (*interp.Interpreter, *strings.Builder) {
	vm := interp.NewInterpreter()
	vm.Capabilities = interp.FullCapabilities()
	var buf strings.Builder

	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			buf.WriteString(interp.ToString(args[0]))
			buf.WriteByte('\n')
		}
		return nil, nil
	})
	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) { return nil, nil })
	vm.RegisterNative("ConsoleError", func(args []any) (any, error) { return nil, nil })
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := interp.ToString(args[0])
		fmtArgs := make([]any, 0, len(args)-1)
		for _, a := range args[1:] {
			fmtArgs = append(fmtArgs, a)
		}
		return fmt.Sprintf(format, fmtArgs...), nil
	})

	interp.RegisterBuiltinPackages(vm)
	return vm, &buf
}

func writeLoaderFile(t *testing.T, vfs *interp.VFS, p, src string) {
	t.Helper()
	if err := vfs.MkdirAll(pathDir(p), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := vfs.WriteFile(p, []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}

// pathDir is a tiny local helper so tests don't need to import path just
// for one call.
func pathDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}
