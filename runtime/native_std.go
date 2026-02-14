// runtime/native_std.go
package runtime

import (
	"fmt"
	"math/rand"
	"strconv"
	"syscall/js"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// ---------------- Console helpers ----------------

// sendMessage tries to call a JS hook `nanoGoPostMessage(msg)` if present to
// stream structured messages to the host. Falls back to console.* when not
// available.
func sendMessage(msg map[string]any) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		// Build a plain JS object instead of relying on js.ValueOf(map)
		obj := js.Global().Get("Object").New()
		for k, v := range msg {
			switch t := v.(type) {
			case string:
				obj.Set(k, t)
			case bool:
				obj.Set(k, t)
			case int:
				obj.Set(k, t)
			case float64:
				obj.Set(k, t)
			default:
				obj.Set(k, fmt.Sprintf("%v", t))
			}
		}
		hook.Invoke(obj)
		return
	}
	// Fallback: map to console
	if t, ok := msg["type"].(string); ok {
		switch t {
		case "log":
			js.Global().Get("console").Call("log", msg["text"])
		case "warn":
			js.Global().Get("console").Call("warn", msg["text"])
		case "error":
			js.Global().Get("console").Call("error", msg["text"])
		default:
			js.Global().Get("console").Call("log", msg)
		}
	} else {
		js.Global().Get("console").Call("log", msg)
	}
}

func ConsoleLog(s string)   { sendMessage(map[string]any{"type": "log", "text": s}) }
func ConsoleWarn(s string)  { sendMessage(map[string]any{"type": "warn", "text": s}) }
func ConsoleError(s string) { sendMessage(map[string]any{"type": "error", "text": s}) }

// ---------------- DOM helpers --------------------

func SetInnerHTML(elementId, html string) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		sendMessage(map[string]any{"type": "dom-setinner", "id": elementId, "html": html})
		return
	}
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", elementId)
	if el.Truthy() {
		el.Set("innerHTML", html)
	}
}

// GetInnerHTML returns the innerHTML of an element (best-effort). If nanoGoPostMessage
// is present we don't try to synchronously fetch from the main thread and return
// an empty string; otherwise we query document directly.
func GetInnerHTML(elementId string) string {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		// Can't synchronously request from host in worker; return empty string
		return ""
	}
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", elementId)
	if el.Truthy() {
		v := el.Get("innerHTML")
		if v.Truthy() {
			return v.String()
		}
	}
	return ""
}

func SetValue(elementId, value string) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		sendMessage(map[string]any{"type": "dom-setvalue", "id": elementId, "value": value})
		return
	}
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", elementId)
	if el.Truthy() {
		el.Set("value", value)
	}
}

func GetValue(elementId string) string {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		return ""
	}
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", elementId)
	if el.Truthy() {
		v := el.Get("value")
		if v.Truthy() {
			return v.String()
		}
	}
	return ""
}

func AddClass(elementId, class string) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		sendMessage(map[string]any{"type": "dom-addclass", "id": elementId, "class": class})
		return
	}
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", elementId)
	if el.Truthy() {
		el.Call("classList").Call("add", class)
	}
}

func RemoveClass(elementId, class string) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		sendMessage(map[string]any{"type": "dom-removeclass", "id": elementId, "class": class})
		return
	}
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", elementId)
	if el.Truthy() {
		el.Call("classList").Call("remove", class)
	}
}

func OpenWindow(url string) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		sendMessage(map[string]any{"type": "open-window", "url": url})
		return
	}
	js.Global().Get("window").Call("open", url, "_blank")
}

func Alert(s string) {
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		sendMessage(map[string]any{"type": "alert", "text": s})
		return
	}
	js.Global().Get("window").Call("alert", s)
}

// ---------------- Canvas binding -----------------

type CanvasBinding struct {
	Canvas    js.Value
	Context2D js.Value
	CellSize  int
	GridW     int
	GridH     int
}

func BindCanvasById(elementId string, cellSize int) CanvasBinding {
	doc := js.Global().Get("document")
	canvas := doc.Call("getElementById", elementId)
	if canvas.IsUndefined() || canvas.IsNull() {
		ConsoleError("Canvas element not found: " + elementId)
	}
	ctx := canvas.Call("getContext", "2d")
	return CanvasBinding{Canvas: canvas, Context2D: ctx, CellSize: cellSize}
}

func (c *CanvasBinding) Size(gridW, gridH int) {
	c.GridW, c.GridH = gridW, gridH
	c.Canvas.Set("width", gridW*c.CellSize)
	c.Canvas.Set("height", gridH*c.CellSize)
	c.Context2D.Call("clearRect", 0, 0, c.Canvas.Get("width").Int(), c.Canvas.Get("height").Int())
}

func (c *CanvasBinding) SetCell(x, y int, alive bool) {
	if c.Context2D.IsUndefined() {
		return
	}
	cs := c.CellSize
	if alive {
		c.Context2D.Call("fillRect", x*cs, y*cs, cs, cs)
	} else {
		c.Context2D.Call("clearRect", x*cs, y*cs, cs, cs)
	}
}

func (c *CanvasBinding) Flush() {
	// No-op (immediate drawing)
}

// ---------------- Simple HTTP + Storage -------------

func HTTPGetText(url string) (string, error) {
	xhr := js.Global().Get("XMLHttpRequest").New()
	xhr.Call("open", "GET", url, false) // sync request (worker-safe)
	xhr.Call("send")
	status := xhr.Get("status").Int()
	if status >= 200 && status < 300 {
		return xhr.Get("responseText").String(), nil
	}
	return "", fmt.Errorf("HTTP status %d", status)
}

func LocalStorageSetItem(key, value string) {
	ls := js.Global().Get("localStorage")
	if ls.Truthy() {
		ls.Call("setItem", key, value)
	}
}

func LocalStorageGetItem(key string) string {
	ls := js.Global().Get("localStorage")
	if !ls.Truthy() {
		return ""
	}
	v := ls.Call("getItem", key)
	if v.Truthy() {
		return v.String()
	}
	return ""
}

// ---------------- Native registrations ----------

// RegisterHostNatives wires host-provided functions to the interpreter globals.
func RegisterHostNatives(vm *interp.Interpreter, canvas *CanvasBinding) {
	rand.Seed(time.Now().UnixNano())

	// Console
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			ConsoleLog(interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) {
		if len(args) > 0 {
			ConsoleWarn(interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("ConsoleError", func(args []any) (any, error) {
		if len(args) > 0 {
			ConsoleError(interp.ToString(args[0]))
		}
		return nil, nil
	})

	// DOM
	vm.RegisterNative("SetInnerHTML", func(args []any) (any, error) {
		if len(args) >= 2 {
			SetInnerHTML(interp.ToString(args[0]), interp.ToString(args[1]))
		}
		return nil, nil
	})

	vm.RegisterNative("GetInnerHTML", func(args []any) (any, error) {
		if len(args) >= 1 {
			return GetInnerHTML(interp.ToString(args[0])), nil
		}
		return "", nil
	})

	vm.RegisterNative("SetValue", func(args []any) (any, error) {
		if len(args) >= 2 {
			SetValue(interp.ToString(args[0]), interp.ToString(args[1]))
		}
		return nil, nil
	})

	vm.RegisterNative("GetValue", func(args []any) (any, error) {
		if len(args) >= 1 {
			return GetValue(interp.ToString(args[0])), nil
		}
		return "", nil
	})

	vm.RegisterNative("AddClass", func(args []any) (any, error) {
		if len(args) >= 2 {
			AddClass(interp.ToString(args[0]), interp.ToString(args[1]))
		}
		return nil, nil
	})

	vm.RegisterNative("RemoveClass", func(args []any) (any, error) {
		if len(args) >= 2 {
			RemoveClass(interp.ToString(args[0]), interp.ToString(args[1]))
		}
		return nil, nil
	})

	vm.RegisterNative("OpenWindow", func(args []any) (any, error) {
		if len(args) >= 1 {
			OpenWindow(interp.ToString(args[0]))
		}
		return nil, nil
	})

	vm.RegisterNative("Alert", func(args []any) (any, error) {
		if len(args) >= 1 {
			Alert(interp.ToString(args[0]))
		}
		return nil, nil
	})

	// Canvas
	vm.RegisterNative("CanvasSize", func(args []any) (any, error) {
		if canvas != nil && canvas.Canvas.Truthy() && len(args) >= 2 {
			w := interp.ToInt(args[0])
			h := interp.ToInt(args[1])
			canvas.Size(w, h)
			return nil, nil
		}
		// If no canvas binding (e.g., running in a worker), post message to host
		if len(args) >= 2 {
			sendMessage(map[string]any{"type": "canvas-size", "w": interp.ToInt(args[0]), "h": interp.ToInt(args[1])})
		}
		return nil, nil
	})
	vm.RegisterNative("CanvasSet", func(args []any) (any, error) {
		if canvas != nil && canvas.Canvas.Truthy() && len(args) >= 3 {
			x := interp.ToInt(args[0])
			y := interp.ToInt(args[1])
			alive := interp.ToBool(args[2])
			canvas.SetCell(x, y, alive)
			return nil, nil
		}
		if len(args) >= 3 {
			sendMessage(map[string]any{"type": "canvas-set", "x": interp.ToInt(args[0]), "y": interp.ToInt(args[1]), "alive": interp.ToBool(args[2])})
		}
		return nil, nil
	})
	vm.RegisterNative("CanvasFlush", func(args []any) (any, error) {
		if canvas != nil && canvas.Canvas.Truthy() {
			canvas.Flush()
			return nil, nil
		}
		sendMessage(map[string]any{"type": "canvas-flush"})
		return nil, nil
	})

	// Random/Time
	vm.RegisterNative("RandFloat", func(args []any) (any, error) { return rand.Float64(), nil })
	vm.RegisterNative("SleepMs", func(args []any) (any, error) {
		if len(args) > 0 {
			time.Sleep(time.Duration(interp.ToInt(args[0])) * time.Millisecond)
		}
		return nil, nil
	})
	vm.RegisterNative("NowMs", func(args []any) (any, error) { return int(time.Now().UnixMilli()), nil })

	// Misc
	vm.RegisterNative("ParseInt", func(args []any) (any, error) {
		if len(args) == 0 {
			return 0, nil
		}
		i, _ := strconv.Atoi(interp.ToString(args[0]))
		return i, nil
	})
	vm.RegisterNative("Assert", func(args []any) (any, error) {
		if len(args) >= 1 && !interp.ToBool(args[0]) {
			msg := "assertion failed"
			if len(args) >= 2 {
				msg = interp.ToString(args[1])
			}
			return nil, interp.NewRuntimeError(msg)
		}
		return nil, nil
	})

	// HTTP & Storage
	vm.RegisterNative("HTTPGetText", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return HTTPGetText(interp.ToString(args[0]))
	})
	vm.RegisterNative("LocalStorageSetItem", func(args []any) (any, error) {
		if len(args) >= 2 {
			LocalStorageSetItem(interp.ToString(args[0]), interp.ToString(args[1]))
		}
		return nil, nil
	})
	vm.RegisterNative("LocalStorageGetItem", func(args []any) (any, error) {
		if len(args) >= 1 {
			return LocalStorageGetItem(interp.ToString(args[0])), nil
		}
		return "", nil
	})

	// Minimal printf used by fmt.Printf native (we rely on Go's fmt since host is Go).
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := interp.ToString(args[0])
		var goArgs []any
		for _, a := range args[1:] {
			goArgs = append(goArgs, a)
		}
		return fmt.Sprintf(format, goArgs...), nil
	})
}
