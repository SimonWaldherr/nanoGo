package runtime

import (
	"syscall/js"
	"testing"
)

func BenchmarkConsoleBridge(b *testing.B) {
	noop := js.Global().Get("Function").New("")
	for _, fast := range []bool{false, true} {
		name := "object"
		if fast {
			name = "scalar"
		}
		b.Run(name, func(b *testing.B) {
			js.Global().Set("nanoGoPostMessage", noop)
			js.Global().Set("nanoGoPostConsole", js.Undefined())
			if fast {
				js.Global().Set("nanoGoPostConsole", noop)
			}
			defer js.Global().Set("nanoGoPostConsole", js.Undefined())
			defer js.Global().Set("nanoGoPostMessage", js.Undefined())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ConsoleLog("Grüße 🌍")
			}
		})
	}
}

func TestConsoleBridge(t *testing.T) {
	for _, fast := range []bool{false, true} {
		var got []string
		hook := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if fast {
				got = append(got, args[0].String()+":"+args[1].String())
			} else {
				got = append(got, args[0].Get("type").String()+":"+args[0].Get("text").String())
			}
			return nil
		})
		js.Global().Set("nanoGoPostMessage", hook)
		if fast {
			js.Global().Set("nanoGoPostConsole", hook)
		}
		ConsoleLog("Grüße 🌍")
		ConsoleWarn("")
		ConsoleError("failure")
		js.Global().Set("nanoGoPostConsole", js.Undefined())
		js.Global().Set("nanoGoPostMessage", js.Undefined())
		hook.Release()
		if len(got) != 3 || got[0] != "log:Grüße 🌍" || got[1] != "warn:" || got[2] != "error:failure" {
			t.Fatal(got)
		}
	}
}
