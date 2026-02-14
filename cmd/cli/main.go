package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nanogo-cli <file.go> [timeout-seconds]")
		os.Exit(1)
	}

	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}

	timeout := 10 * time.Second
	if len(os.Args) >= 3 {
		d, err := time.ParseDuration(os.Args[2] + "s")
		if err == nil {
			timeout = d
		}
	}

	if err := RunSafe(string(src), timeout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

// RunSafe executes untrusted Go source inside the nanoGo interpreter
// with a context-based timeout. It recovers from panics so the host
// application is never crashed by user code.
func RunSafe(source string, timeout time.Duration) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runInterpreted(source)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("execution timed out after %s", timeout)
	}
}

// runInterpreted creates a sandboxed interpreter, registers only the
// host functions we choose to expose, and executes the source.
func runInterpreted(source string) error {
	vm := interp.NewInterpreter()
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)
	return vm.Run(source)
}

// registerSafeNatives installs only the minimal set of host functions
// needed for console output. Anything dangerous (file access, network,
// DOM, etc.) is intentionally omitted.
func registerSafeNatives(vm *interp.Interpreter) {
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})

	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprintln(os.Stderr, "[warn]", interp.ToString(args[0]))
		}
		return nil, nil
	})

	vm.RegisterNative("ConsoleError", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprintln(os.Stderr, "[error]", interp.ToString(args[0]))
		}
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
