// cmd/mcp/main.go — nanoGo MCP (Model Context Protocol) server.
//
// The server reads JSON-RPC 2.0 messages from stdin (one per line) and writes
// responses to stdout, following the MCP stdio transport specification.
// Log/debug output goes to stderr so it never pollutes the protocol stream.
//
// Exposed MCP tools:
//
//	run_code   – execute Go source in the nanoGo interpreter; returns stdout
//	fmt_code   – gofmt-format Go source
//	vet_code   – static-analysis checks on Go source
//	vfs_read   – read a file from the session virtual filesystem
//	vfs_write  – write a file to the session virtual filesystem
//	vfs_list   – list directory contents in the virtual filesystem
//	vfs_mkdir  – create a directory in the virtual filesystem
//	vfs_remove – remove a file or directory from the virtual filesystem
//
// The server maintains a single VFS per process lifetime; code executed with
// run_code can read/write files that were created with the vfs_* tools.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// ── JSON-RPC 2.0 types ──────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── MCP protocol types ───────────────────────────────────────────────────────

type mcpInitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// ── server state ─────────────────────────────────────────────────────────────

var sessionVFS = interp.NewVFS()

// ── entry point ──────────────────────────────────────────────────────────────

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase scanner buffer for large code payloads (up to 4 MiB).
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	encoder := json.NewEncoder(os.Stdout)

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[nanoGo-mcp] "+format+"\n", args...)
	}

	logf("nanoGo MCP server starting (stdio transport)")

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			logf("parse error: %v", err)
			// Send a parse-error response; we have no id so use null.
			_ = encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &jsonRPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		// Notifications (no id) are fire-and-forget; we only acknowledge them in logs.
		isNotification := req.ID == nil || string(req.ID) == "null"

		result, rpcErr := handleMethod(req.Method, req.Params)

		if isNotification {
			// Do not send a response for notifications.
			logf("notification %s handled", req.Method)
			continue
		}

		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			resp.Result = result
		}
		if err := encoder.Encode(resp); err != nil {
			logf("encode error: %v", err)
		}
	}
	if err := scanner.Err(); err != nil {
		logf("scanner error: %v", err)
		os.Exit(1)
	}
}

// handleMethod dispatches a JSON-RPC method to the appropriate handler.
func handleMethod(method string, rawParams json.RawMessage) (any, *jsonRPCError) {
	switch method {
	case "initialize":
		return handleInitialize(rawParams)
	case "notifications/initialized":
		return nil, nil
	case "tools/list":
		return handleToolsList()
	case "tools/call":
		return handleToolCall(rawParams)
	case "ping":
		return map[string]any{}, nil
	default:
		return nil, &jsonRPCError{Code: -32601, Message: "Method not found: " + method}
	}
}

// ── initialize ────────────────────────────────────────────────────────────────

func handleInitialize(_ json.RawMessage) (any, *jsonRPCError) {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "nanoGo",
			"version": "0.1.0",
		},
	}, nil
}

// ── tools/list ────────────────────────────────────────────────────────────────

func handleToolsList() (any, *jsonRPCError) {
	tools := []mcpTool{
		{
			Name:        "run_code",
			Description: "Execute Go source code in the nanoGo interpreter. The code must be a complete package main with a main() function. Output written via fmt.Println or the browser/os packages is captured and returned.",
			InputSchema: schema(map[string]any{
				"code":    prop("string", "Complete Go source code to execute (must include package main)"),
				"timeout": prop("integer", "Execution timeout in seconds (default 10, max 60)"),
			}, "code"),
		},
		{
			Name:        "fmt_code",
			Description: "Format Go source code using gofmt rules. Returns the formatted source or an error message.",
			InputSchema: schema(map[string]any{
				"code": prop("string", "Go source code to format"),
			}, "code"),
		},
		{
			Name:        "vet_code",
			Description: "Run basic static analysis (go vet subset) on Go source code. Returns a list of issues or 'ok' if none found.",
			InputSchema: schema(map[string]any{
				"code": prop("string", "Go source code to analyse"),
			}, "code"),
		},
		{
			Name:        "vfs_read",
			Description: "Read a file from the session virtual filesystem. Paths follow Unix conventions (e.g. /tmp/data.txt).",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Absolute or relative path of the file to read"),
			}, "path"),
		},
		{
			Name:        "vfs_write",
			Description: "Write (create or overwrite) a file in the session virtual filesystem.",
			InputSchema: schema(map[string]any{
				"path":    prop("string", "Absolute or relative path of the file to write"),
				"content": prop("string", "Content to write"),
			}, "path", "content"),
		},
		{
			Name:        "vfs_list",
			Description: "List the contents of a directory in the session virtual filesystem.",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Directory path to list (default '/')"),
			}),
		},
		{
			Name:        "vfs_mkdir",
			Description: "Create a directory (and all parents) in the session virtual filesystem.",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Directory path to create"),
			}, "path"),
		},
		{
			Name:        "vfs_remove",
			Description: "Remove a file or directory from the session virtual filesystem. Directories must be empty unless recursive is true.",
			InputSchema: schema(map[string]any{
				"path":      prop("string", "Path to remove"),
				"recursive": prop("boolean", "Remove directory and all contents (default false)"),
			}, "path"),
		},
	}
	return map[string]any{"tools": tools}, nil
}

// prop builds a simple JSON-Schema property descriptor.
func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

// schema builds a minimal JSON-Schema object descriptor for tool input.
func schema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// ── tools/call ────────────────────────────────────────────────────────────────

func handleToolCall(rawParams json.RawMessage) (any, *jsonRPCError) {
	var params mcpToolCallParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid params: " + err.Error()}
	}

	// Decode the tool arguments as a generic map.
	var args map[string]any
	if len(params.Arguments) > 0 {
		_ = json.Unmarshal(params.Arguments, &args)
	}
	if args == nil {
		args = map[string]any{}
	}

	strArg := func(key, def string) string {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return def
	}
	boolArg := func(key string) bool {
		if v, ok := args[key]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
		return false
	}
	intArg := func(key string, def int) int {
		if v, ok := args[key]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			}
		}
		return def
	}

	var text string
	var isError bool

	switch params.Name {
	case "run_code":
		code := strArg("code", "")
		if code == "" {
			text = "error: 'code' argument is required"
			isError = true
			break
		}
		timeoutSec := intArg("timeout", 10)
		if timeoutSec <= 0 || timeoutSec > 60 {
			timeoutSec = 10
		}
		out, err := runCode(code, sessionVFS, time.Duration(timeoutSec)*time.Second)
		if err != nil {
			text = out
			if text != "" {
				text += "\n"
			}
			text += "error: " + err.Error()
			isError = true
		} else {
			text = out
		}

	case "fmt_code":
		code := strArg("code", "")
		if code == "" {
			text = "error: 'code' argument is required"
			isError = true
			break
		}
		formatted, err := interp.FormatSource(code)
		if err != nil {
			text = "error: " + err.Error()
			isError = true
		} else {
			text = formatted
		}

	case "vet_code":
		code := strArg("code", "")
		if code == "" {
			text = "error: 'code' argument is required"
			isError = true
			break
		}
		issues, err := interp.VetSource(code)
		if err != nil {
			text = "error: " + err.Error()
			isError = true
		} else if len(issues) == 0 {
			text = "ok"
		} else {
			var sb strings.Builder
			for _, iss := range issues {
				sb.WriteString(iss.String())
				sb.WriteByte('\n')
			}
			text = sb.String()
		}

	case "vfs_read":
		p := strArg("path", "")
		if p == "" {
			text = "error: 'path' argument is required"
			isError = true
			break
		}
		data, err := sessionVFS.ReadFile(p)
		if err != nil {
			text = "error: " + err.Error()
			isError = true
		} else {
			text = string(data)
		}

	case "vfs_write":
		p := strArg("path", "")
		content := strArg("content", "")
		if p == "" {
			text = "error: 'path' argument is required"
			isError = true
			break
		}
		if err := sessionVFS.WriteFile(p, []byte(content), 0644); err != nil {
			text = "error: " + err.Error()
			isError = true
		} else {
			text = fmt.Sprintf("wrote %d bytes to %s", len(content), p)
		}

	case "vfs_list":
		p := strArg("path", "/")
		entries, err := sessionVFS.ReadDir(p)
		if err != nil {
			text = "error: " + err.Error()
			isError = true
		} else {
			var sb strings.Builder
			for _, e := range entries {
				kind := "-"
				if e.IsDir {
					kind = "d"
				}
				sb.WriteString(fmt.Sprintf("%s  %s\n", kind, e.Name))
			}
			if sb.Len() == 0 {
				text = "(empty directory)"
			} else {
				text = sb.String()
			}
		}

	case "vfs_mkdir":
		p := strArg("path", "")
		if p == "" {
			text = "error: 'path' argument is required"
			isError = true
			break
		}
		if err := sessionVFS.MkdirAll(p, 0755); err != nil {
			text = "error: " + err.Error()
			isError = true
		} else {
			text = "created " + p
		}

	case "vfs_remove":
		p := strArg("path", "")
		if p == "" {
			text = "error: 'path' argument is required"
			isError = true
			break
		}
		var err error
		if boolArg("recursive") {
			err = sessionVFS.RemoveAll(p)
		} else {
			err = sessionVFS.Remove(p)
		}
		if err != nil {
			text = "error: " + err.Error()
			isError = true
		} else {
			text = "removed " + p
		}

	default:
		return nil, &jsonRPCError{Code: -32602, Message: "Unknown tool: " + params.Name}
	}

	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: text}},
		IsError: isError,
	}, nil
}

// runCode executes Go source code in a fresh Interpreter that shares the session VFS.
// It captures all fmt.Print*/browser.ConsoleLog output and returns it as a string.
func runCode(source string, vfs *interp.VFS, timeout time.Duration) (output string, retErr error) {
	var buf strings.Builder
	var bufMu sync.Mutex

	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	vm := interp.NewInterpreterWithVFS(vfs)
	write := func(prefix string, args []any) {
		if len(args) == 0 {
			return
		}
		bufMu.Lock()
		buf.WriteString(prefix)
		buf.WriteString(interp.ToString(args[0]))
		buf.WriteByte('\n')
		bufMu.Unlock()
	}
	// Guest goroutines can log concurrently, so capture output behind a mutex.
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) { write("", args); return nil, nil })
	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) { write("[warn] ", args); return nil, nil })
	vm.RegisterNative("ConsoleError", func(args []any) (any, error) { write("[error] ", args); return nil, nil })
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
	retErr = vm.RunContext(ctx, source)
	bufMu.Lock()
	output = buf.String()
	bufMu.Unlock()
	if errors.Is(retErr, context.DeadlineExceeded) {
		return output, fmt.Errorf("execution timed out after %s: %w", timeout, retErr)
	}
	return output, retErr
}
