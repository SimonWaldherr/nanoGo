package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"

	"simonwaldherr.de/go/nanogo/interp"
)

const defaultEvaluationTimeout = 10 * time.Second

func main() {
	vm := interp.NewInterpreter()
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)

	fmt.Println("nanoGo REPL — enter declarations (func, var, const, type, import) or statements.")
	fmt.Println("Commands: :fmt <code>  :vet <code>  :timeout <duration|off>  Ctrl-C to stop  Ctrl-D to exit.")
	reader := bufio.NewReader(os.Stdin)
	timeout := defaultEvaluationTimeout
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	stopSignals := make(chan struct{})
	defer close(stopSignals)
	go func() {
		for {
			select {
			case <-stopSignals:
				return
			case <-interrupts:
				if vm.Kill() {
					fmt.Fprintln(os.Stderr, "\ninterrupted")
				}
			}
		}
	}()

	for {
		fmt.Print("ng> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		line = strings.TrimSpace(line)
		// Skip blank lines and any stray "package" declarations — the REPL
		// automatically wraps all input in "package main", so users should
		// never need to type it at the prompt.
		if line == "" || strings.HasPrefix(line, "package ") {
			continue
		}

		// Special REPL commands.
		if strings.HasPrefix(line, ":timeout") {
			value := strings.TrimSpace(strings.TrimPrefix(line, ":timeout"))
			if value == "" {
				fmt.Println("timeout:", timeoutDescription(timeout))
				continue
			}
			if value == "off" {
				timeout = 0
				fmt.Println("timeout disabled")
				continue
			}
			parsed, parseErr := time.ParseDuration(value)
			if parseErr != nil || parsed <= 0 {
				fmt.Println("usage: :timeout <positive duration|off> (for example: :timeout 2s)")
				continue
			}
			timeout = parsed
			fmt.Println("timeout:", timeoutDescription(timeout))
			continue
		}

		if strings.HasPrefix(line, ":fmt ") || line == ":fmt" {
			code := strings.TrimSpace(strings.TrimPrefix(line, ":fmt"))
			if code == "" {
				fmt.Println("usage: :fmt <go-code>")
				continue
			}
			src := "package main\n" + code + "\n"
			formatted, fmtErr := interp.FormatSource(src)
			if fmtErr != nil {
				fmt.Println("fmt error:", fmtErr)
			} else {
				// Strip the "package main\n" prefix we added.
				formatted = strings.TrimPrefix(formatted, "package main\n")
				fmt.Print(formatted)
			}
			continue
		}

		if strings.HasPrefix(line, ":vet ") || line == ":vet" {
			code := strings.TrimSpace(strings.TrimPrefix(line, ":vet"))
			if code == "" {
				fmt.Println("usage: :vet <go-code>")
				continue
			}
			src := "package main\nfunc main() {\n" + code + "\n}\n"
			issues, vetErr := interp.VetSource(src)
			if vetErr != nil {
				fmt.Println("parse error:", vetErr)
			} else if len(issues) == 0 {
				fmt.Println("ok")
			} else {
				for _, issue := range issues {
					fmt.Println(issue)
				}
			}
			continue
		}

		if strings.HasPrefix(line, "import ") {
			src := "package main\n" + line + "\nfunc main() {}\n"
			if err := runREPLSource(vm, timeout, src); err != nil {
				fmt.Println("error:", err)
			}
			continue
		}

		if looksLikeDecl(line) {
			src := buildDeclSource(line)
			if err := runREPLSource(vm, timeout, src); err != nil {
				fmt.Println("error:", err)
			}
			continue
		}

		// Convert simple short-variable declarations (x := expr) to top-level var
		// declarations so their values persist in the VM's global state.
		if converted, ok := tryConvertShortVarDecl(line); ok {
			src := buildDeclSource(converted)
			if err := runREPLSource(vm, timeout, src); err != nil {
				fmt.Println("error:", err)
			}
			continue
		}

		// Regular statement — executed in main() context with access to all globals.
		src := buildStmtSource(line)
		if err := runREPLSource(vm, timeout, src); err != nil {
			fmt.Println("error:", err)
		}
	}
}

func runREPLSource(vm *interp.Interpreter, timeout time.Duration, src string) error {
	if timeout <= 0 {
		return vm.RunContext(context.Background(), src)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return vm.RunContext(ctx, src)
}

func timeoutDescription(timeout time.Duration) string {
	if timeout <= 0 {
		return "off"
	}
	return timeout.String()
}

// looksLikeDecl reports whether a line is a top-level Go declaration.
func looksLikeDecl(s string) bool {
	trim := strings.TrimSpace(s)
	return strings.HasPrefix(trim, "func ") ||
		strings.HasPrefix(trim, "type ") ||
		strings.HasPrefix(trim, "const ") ||
		strings.HasPrefix(trim, "var ")
}

// tryConvertShortVarDecl converts a simple "ident := expr" line to "var ident = expr"
// so the variable is declared at the top level and persists in the VM.
func tryConvertShortVarDecl(line string) (string, bool) {
	trim := strings.TrimSpace(line)
	idx := strings.Index(trim, ":=")
	if idx <= 0 {
		return "", false
	}
	lhs := strings.TrimSpace(trim[:idx])
	rhs := strings.TrimSpace(trim[idx+2:])
	// Only convert when LHS is a single simple identifier.
	if !isSimpleIdent(lhs) {
		return "", false
	}
	return "var " + lhs + " = " + rhs, true
}

func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if i == 0 {
			if !unicode.IsLetter(c) && c != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
				return false
			}
		}
	}
	return true
}

// buildDeclSource wraps a single declaration in a minimal package main with a no-op main().
// Imports are evaluated once and their package aliases persist in the VM, so
// subsequent declarations do not repeatedly parse every prior import line.
func buildDeclSource(decl string) string {
	var b strings.Builder
	b.Grow(len(decl) + 32)
	b.WriteString("package main\n")
	b.WriteString(decl)
	b.WriteString("\nfunc main() {}\n")
	return b.String()
}

// buildStmtSource wraps a statement in a package main / main() so it runs in the
// persistent VM's global context. Imported aliases are already VM globals.
func buildStmtSource(stmt string) string {
	var b strings.Builder
	b.Grow(len(stmt) + 32)
	b.WriteString("package main\n")
	b.WriteString("func main() {\n")
	b.WriteString(stmt)
	b.WriteString("\n}\n")
	return b.String()
}

// registerSafeNatives installs the minimal safe host functions.
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
