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

	for _, native := range []string{"SetInnerHTML", "CanvasSize", "CanvasSet", "CanvasSetLevel", "CanvasFlush"} {
		vm.RegisterNative(native, func(args []any) (any, error) { return nil, nil })
	}
	vm.RegisterNative("HTTPGetText", func(args []any) (any, error) {
		url := ""
		if len(args) > 0 {
			url = ToString(args[0])
		}
		if strings.Contains(url, "nanogo-missing-route") {
			return "", NewRuntimeError("HTTP status 404")
		}
		return "{\"id\":1,\"title\":\"nanoGo example\"}", nil
	})
	vm.RegisterNative("HTTPPostText", func(args []any) (any, error) {
		return "{\"id\":201,\"accepted\":true}", nil
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

// renderExampleTemplate mirrors the small subset of JavaScript template
// escapes used in web/examples.js. The browser evaluates those escapes before
// the source reaches nanoGo; tests must do the same or a single `\n` in the
// JS file looks valid here but becomes an illegal literal newline in Go at
// runtime. In particular, a source-level `\\n` becomes Go's `\n` escape.
func renderExampleTemplate(src string) string {
	var out strings.Builder
	out.Grow(len(src))
	for i := 0; i < len(src); i++ {
		if src[i] != '\\' || i+1 == len(src) {
			out.WriteByte(src[i])
			continue
		}
		i++
		switch src[i] {
		case '\\', '`':
			out.WriteByte(src[i])
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		default:
			// Preserve uncommon/invalid JS escapes verbatim; the Go parser then
			// reports them just as the browser-delivered program would.
			out.WriteByte('\\')
			out.WriteByte(src[i])
		}
	}
	return out.String()
}

func TestWebExamples(t *testing.T) {
	for name, source := range webExamples(t) {
		t.Run(name, func(t *testing.T) {
			vm, output := newWebExampleVM()
			if err := vm.Run(renderExampleTemplate(source)); err != nil {
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
			if name == "HTTP Client + Storage" && strings.Contains(output.String(), "stored lastFetchLen: \n") {
				t.Errorf("storage example did not read back the stored value: %q", output.String())
			}
		})
	}
}
