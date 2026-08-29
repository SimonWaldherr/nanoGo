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
	// cells is used only when the interpreter runs in a Web Worker: one
	// palette level per grid cell, row-major. Crossing Go<->JS once per
	// painted cell is far more expensive than the actual rendering work, so
	// paints land here and the whole grid ships as one byte buffer per
	// explicit Flush (or at the end of a run). It persists across flushes,
	// so demos that only repaint what changed keep working.
	cells []byte
	dirty bool
}

// canvasPalette provides eight stable levels for direct DOM-bound canvases.
// Level 0 is the background; level 1 remains the established nanoGo green so
// existing CanvasSet demos keep their visual identity. Levels 2..7 are used
// by palette-aware examples such as Mandelbrot.
var canvasPalette = [...]string{"#080d13", "#10b981", "#0ea5e9", "#2563eb", "#7c3aed", "#ec4899", "#f97316", "#facc15"}

func (c *CanvasBinding) isBound() bool {
	return c != nil && c.Canvas.Truthy() && !c.Context2D.IsUndefined()
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
	if !c.isBound() {
		// A resize establishes a new coordinate system, so all previously
		// queued cells must reach the host before it.
		c.Flush()
		n := gridW * gridH
		if n < 0 {
			n = 0
		}
		c.cells = make([]byte, n)
		c.dirty = false
		sendMessage(map[string]any{"type": "canvas-size", "w": gridW, "h": gridH})
		return
	}
	c.Canvas.Set("width", gridW*c.CellSize)
	c.Canvas.Set("height", gridH*c.CellSize)
	c.Context2D.Call("clearRect", 0, 0, c.Canvas.Get("width").Int(), c.Canvas.Get("height").Int())
}

func (c *CanvasBinding) SetCell(x, y int, alive bool) {
	level := 0
	if alive {
		level = 1
	}
	c.SetCellLevel(x, y, level)
}

// SetCellLevel paints one cell with a compact palette level from 0 to 7.
// It retains CanvasSet's batched transport format while enabling demos that
// need more information than a binary on/off pixel.
func (c *CanvasBinding) SetCellLevel(x, y, level int) {
	if level < 0 {
		level = 0
	} else if level >= len(canvasPalette) {
		level = len(canvasPalette) - 1
	}
	if !c.isBound() {
		// One byte store per paint, no allocation and no encoding: the grid
		// itself is the wire format, so a frame costs a single JS callback
		// regardless of how many cells a Game-of-Life generation touches.
		if x < 0 || y < 0 || x >= c.GridW || y >= c.GridH || len(c.cells) < c.GridW*c.GridH {
			return
		}
		c.cells[y*c.GridW+x] = byte(level)
		c.dirty = true
		return
	}
	cs := c.CellSize
	c.Context2D.Set("fillStyle", canvasPalette[level])
	c.Context2D.Call("fillRect", x*cs, y*cs, cs, cs)
}

func (c *CanvasBinding) Flush() {
	if c == nil || !c.dirty || len(c.cells) == 0 {
		return
	}
	// Avoid sendMessage's generic map conversion: this is the hot worker
	// transport path, and its payload is already a flat byte grid.
	hook := js.Global().Get("nanoGoPostMessage")
	if hook.Truthy() {
		// A fresh Uint8Array per frame: the worker may hold this message in
		// its outbound batch, so the buffer must not change underneath it.
		buf := js.Global().Get("Uint8Array").New(len(c.cells))
		js.CopyBytesToJS(buf, c.cells)
		obj := js.Global().Get("Object").New()
		obj.Set("type", "canvas-frame")
		obj.Set("w", c.GridW)
		obj.Set("h", c.GridH)
		obj.Set("cells", buf)
		hook.Invoke(obj)
	}
	c.dirty = false
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

func HTTPPostText(url, body, contentType string) (string, error) {
	xhr := js.Global().Get("XMLHttpRequest").New()
	xhr.Call("open", "POST", url, false)
	xhr.Call("setRequestHeader", "Content-Type", contentType)
	xhr.Call("send", body)
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
		return
	}
	workerStorage().Call("set", key, value)
}

func LocalStorageGetItem(key string) string {
	ls := js.Global().Get("localStorage")
	if ls.Truthy() {
		v := ls.Call("getItem", key)
		if v.Truthy() {
			return v.String()
		}
		return ""
	}
	v := workerStorage().Call("get", key)
	if v.Truthy() {
		return v.String()
	}
	return ""
}

// workerStorage provides session-scoped storage when the interpreter runs in a
// Web Worker, where the browser's synchronous localStorage API is unavailable.
// The map survives subsequent playground runs in the same worker.
func workerStorage() js.Value {
	store := js.Global().Get("nanoGoWorkerStorage")
	if store.Truthy() {
		return store
	}
	store = js.Global().Get("Map").New()
	js.Global().Set("nanoGoWorkerStorage", store)
	return store
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
		if canvas != nil && len(args) >= 2 {
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
		if canvas != nil && len(args) >= 3 {
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
	vm.RegisterNative("CanvasSetLevel", func(args []any) (any, error) {
		if canvas != nil && len(args) >= 3 {
			canvas.SetCellLevel(interp.ToInt(args[0]), interp.ToInt(args[1]), interp.ToInt(args[2]))
			return nil, nil
		}
		if len(args) >= 3 {
			sendMessage(map[string]any{"type": "canvas-set-level", "x": interp.ToInt(args[0]), "y": interp.ToInt(args[1]), "level": interp.ToInt(args[2])})
		}
		return nil, nil
	})
	vm.RegisterNative("CanvasFlush", func(args []any) (any, error) {
		if canvas != nil {
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

	// HTTP & Storage. Registered with RegisterInternalNative (not
	// RegisterNative): these must only be reachable through
	// http.GetText/PostText/storage.SetItem/GetItem's own wrapper functions
	// in interp/packages.go, never as bare guest-callable identifiers.
	vm.RegisterInternalNative("HTTPGetText", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return HTTPGetText(interp.ToString(args[0]))
	})
	vm.RegisterInternalNative("HTTPPostText", func(args []any) (any, error) {
		if len(args) < 2 {
			return "", nil
		}
		contentType := "application/json"
		if len(args) >= 3 {
			contentType = interp.ToString(args[2])
		}
		return HTTPPostText(interp.ToString(args[0]), interp.ToString(args[1]), contentType)
	})
	vm.RegisterInternalNative("LocalStorageSetItem", func(args []any) (any, error) {
		if len(args) >= 2 {
			LocalStorageSetItem(interp.ToString(args[0]), interp.ToString(args[1]))
		}
		return nil, nil
	})
	vm.RegisterInternalNative("LocalStorageGetItem", func(args []any) (any, error) {
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
