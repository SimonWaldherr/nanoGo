package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	fmt.Println("nanoGo REPL — enter declarations (func, var, const, type, import) or statements. Ctrl-D to exit.")
	reader := bufio.NewReader(os.Stdin)
	decls := strings.Builder{}

	for {
		fmt.Print("ng> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if looksLikeDecl(line) {
			decls.WriteString(line)
			decls.WriteString("\n")
			continue
		}

		src := buildSource(decls.String(), line)
		if err := runSource(src); err != nil {
			fmt.Println("error:", err)
		}
	}
}

func looksLikeDecl(s string) bool {
	trim := strings.TrimSpace(s)
	return strings.HasPrefix(trim, "func ") || strings.HasPrefix(trim, "type ") || strings.HasPrefix(trim, "const ") || strings.HasPrefix(trim, "var ") || strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "package ")
}

func buildSource(decls, stmt string) string {
	// Build a small program that contains cumulative declarations and a single replMain
	// function which executes the provided statement.
	var b strings.Builder
	b.WriteString("package main\n\n")
	if strings.TrimSpace(decls) != "" {
		b.WriteString(decls)
		b.WriteString("\n")
	}
	b.WriteString("func replMain() {\n")
	b.WriteString(stmt)
	b.WriteString("\n}\n\nfunc main() { replMain() }\n")
	return b.String()
}

func runSource(src string) error {
	vm := interp.NewInterpreter()
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)
	return vm.Run(src)
}

// registerSafeNatives mirrors the minimal safe host functions used in the CLI.
func registerSafeNatives(vm *interp.Interpreter) {
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) {
		return nil, nil
	})
	vm.RegisterNative("ConsoleError", func(args []any) (any, error) {
		return nil, nil
	})
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
}
