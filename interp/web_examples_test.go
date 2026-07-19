package interp

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var webExamplePattern = regexp.MustCompile("(?ms)(?:^\\s*|,)\"([^\"]+)\": `([^`]*)`")

func webExamples(t *testing.T) map[string]string {
	t.Helper()
	contents, err := os.ReadFile("../web/examples.js")
	if err != nil {
		t.Fatalf("read web examples: %v", err)
	}

	matches := webExamplePattern.FindAllStringSubmatch(string(contents), -1)
	examples := make(map[string]string, len(matches))
	for _, match := range matches {
		examples[match[1]] = match[2]
	}
	if len(examples) == 0 {
		t.Fatal("no web examples found")
	}
	return examples
}

func newWebExampleVM() (*Interpreter, *strings.Builder) {
	vm, output := newTestVM()
	storage := map[string]string{}

	for _, native := range []string{"SetInnerHTML", "CanvasSize", "CanvasSet", "CanvasFlush"} {
		vm.RegisterNative(native, func(args []any) (any, error) { return nil, nil })
	}
	vm.RegisterNative("HTTPGetText", func(args []any) (any, error) {
		return "window.EXAMPLES = {};", nil
	})
	vm.RegisterNative("LocalStorageSetItem", func(args []any) (any, error) {
		if len(args) >= 2 {
			storage[ToString(args[0])] = ToString(args[1])
		}
		return nil, nil
	})
	vm.RegisterNative("LocalStorageGetItem", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return storage[ToString(args[0])], nil
	})
	return vm, output
}

func TestWebExamples(t *testing.T) {
	for name, source := range webExamples(t) {
		t.Run(name, func(t *testing.T) {
			vm, output := newWebExampleVM()
			if err := vm.Run(source); err != nil {
				t.Fatalf("web example failed: %v", err)
			}
			if name == "Timer Ticker" {
				for _, want := range []string{"Timer fired", "tick 0", "tick 1", "tick 2", "done"} {
					if !strings.Contains(output.String(), want) {
						t.Errorf("timer output missing %q: %q", want, output.String())
					}
				}
			}
			if name == "HTTP + Storage" && strings.Contains(output.String(), "stored lastFetchLen: \n") {
				t.Errorf("storage example did not read back the stored value: %q", output.String())
			}
		})
	}
}
