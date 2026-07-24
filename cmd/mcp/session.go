// cmd/mcp/session.go — client sessions and the Streamable HTTP transport.
//
// A session owns one in-memory VFS and is the unit of isolation between MCP
// clients. The stdio transport (see main.go) serves a single process-wide
// session (defaultSession) for its whole lifetime. The Streamable HTTP
// transport allocates one session per `initialize` call, keyed by the
// Mcp-Session-Id header it returns, and reclaims sessions that have been idle
// for longer than sessionIdleTimeout so a long-lived server does not leak
// workspaces for clients that disconnect without a DELETE.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

const (
	// sessionIDHeader carries the per-client session identifier on the HTTP
	// transport, per the MCP Streamable HTTP spec.
	sessionIDHeader = "Mcp-Session-Id"
	// sessionIdleTimeout is how long an HTTP session may go untouched before
	// the background reaper discards it and its VFS.
	sessionIdleTimeout = 30 * time.Minute
	// sessionReapInterval is how often the reaper scans for idle sessions.
	sessionReapInterval = 5 * time.Minute
	// maxHTTPRequestBytes bounds a single JSON-RPC request body, matching the
	// stdio scanner's 4 MiB limit so large code payloads still fit.
	maxHTTPRequestBytes = 4 << 20
)

// session is one client's isolated workspace. Its VFS is never the host disk;
// it exists only for the life of the session.
type session struct {
	id  string
	vfs *interp.VFS

	mu       sync.Mutex
	lastSeen time.Time
}

// defaultSession is the single implicit session used by the stdio transport
// and by the test suite. HTTP clients get their own sessions from a store.
var defaultSession = newSession("stdio")

// newSession creates a session with a fresh, empty VFS.
func newSession(id string) *session {
	return &session{
		id:       id,
		vfs:      interp.NewVFS(),
		lastSeen: time.Now(),
	}
}

// touch records that the session was just used, deferring idle reclamation.
func (s *session) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

// idleSince reports when the session was last touched.
func (s *session) idleSince() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeen
}

// logf writes a diagnostic line to stderr. The stdio transport keeps its own
// local logger; this package-level one serves the HTTP transport.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[nanoGo-mcp] "+format+"\n", args...)
}

// ── session store (HTTP transport) ───────────────────────────────────────────

// sessionStore holds the live HTTP sessions keyed by their Mcp-Session-Id.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]*session)}
}

// create allocates a new session with a freshly generated identifier.
func (store *sessionStore) create() *session {
	sess := newSession(newSessionID())
	store.mu.Lock()
	store.sessions[sess.id] = sess
	store.mu.Unlock()
	return sess
}

// get returns the session for id, or (nil, false) if there is none.
func (store *sessionStore) get(id string) (*session, bool) {
	if id == "" {
		return nil, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	sess, ok := store.sessions[id]
	return sess, ok
}

// remove deletes the session for id and reports whether one existed.
func (store *sessionStore) remove(id string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.sessions[id]; !ok {
		return false
	}
	delete(store.sessions, id)
	return true
}

// reap discards sessions untouched for longer than sessionIdleTimeout and
// returns how many were removed.
func (store *sessionStore) reap(now time.Time) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	removed := 0
	for id, sess := range store.sessions {
		if now.Sub(sess.idleSince()) > sessionIdleTimeout {
			delete(store.sessions, id)
			removed++
		}
	}
	return removed
}

// newSessionID returns an unguessable session identifier. crypto/rand failing
// is extraordinary; fall back to a time-based id rather than crashing.
func newSessionID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

// ── HTTP transport ────────────────────────────────────────────────────────────

// runHTTPServer implements the MCP Streamable HTTP transport: a single /mcp
// endpoint for JSON-RPC (POST) and session teardown (DELETE), plus unauthenticated
// GET / and GET /healthz probes. CORS is open so browser-based clients can reach
// it directly; put it behind your own auth/proxy before exposing it beyond a
// trusted network.
func runHTTPServer(addr string) {
	store := newSessionStore()

	// Background reaper for idle sessions.
	go func() {
		ticker := time.NewTicker(sessionReapInterval)
		defer ticker.Stop()
		for now := range ticker.C {
			if removed := store.reap(now); removed > 0 {
				logf("reclaimed %d idle session(s)", removed)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		handleHTTPMCP(store, w, r)
	})
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/", handleRoot)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// ReadTimeout/WriteTimeout are intentionally left unset: a single tool
		// call may run up to the caller-configured 60 s execution limit before
		// the server writes its response.
	}

	logf("nanoGo MCP server starting (HTTP transport) on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logf("http server error: %v", err)
		os.Exit(1)
	}
}

// handleHTTPMCP serves the /mcp endpoint for one request.
func handleHTTPMCP(store *sessionStore, w http.ResponseWriter, r *http.Request) {
	setCORS(w)

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodDelete:
		id := r.Header.Get(sessionIDHeader)
		if id == "" {
			http.Error(w, sessionIDHeader+" header is required", http.StatusBadRequest)
			return
		}
		if store.remove(id) {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, "unknown session", http.StatusNotFound)
		}
		return
	case http.MethodGet:
		// This server pushes no server-initiated messages, so it offers no
		// standalone SSE stream here. Returning 405 is spec-compliant; clients
		// use POST for all requests.
		w.Header().Set("Allow", "POST, DELETE, OPTIONS")
		http.Error(w, "this endpoint accepts POST for JSON-RPC and DELETE to end a session", http.StatusMethodNotAllowed)
		return
	case http.MethodPost:
		// Handled below.
	default:
		w.Header().Set("Allow", "POST, DELETE, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPRequestBytes))
	if err != nil {
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: -32700, Message: "Parse error"},
		})
		return
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" || !validRequestID(req.ID) {
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32600, Message: "Invalid Request"},
		})
		return
	}

	// Resolve (or, for initialize, allocate) the calling client's session.
	var sess *session
	if req.Method == "initialize" {
		sess = store.create()
		w.Header().Set(sessionIDHeader, sess.id)
	} else {
		var ok bool
		if sess, ok = store.get(r.Header.Get(sessionIDHeader)); !ok {
			writeJSONRPC(w, http.StatusNotFound, jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32001, Message: "No active session; send initialize first"},
			})
			return
		}
	}

	// Notifications (no ID) are acknowledged without a JSON-RPC response body.
	isNotification := req.ID == nil
	result, rpcErr := handleMethod(req.Method, req.Params, sess)
	if isNotification {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	writeJSONRPC(w, http.StatusOK, resp)
}

// handleHealth answers GET /healthz with a small JSON status document.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"server":  serverName,
		"version": serverVersion,
	})
}

// handleRoot answers GET / with a plain-text description for quick curl checks.
func handleRoot(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "%s MCP server %s\nPOST /mcp for JSON-RPC, DELETE /mcp to end a session, GET /healthz for health.\n",
		serverName, serverVersion)
}

// setCORS applies the open CORS policy documented for the HTTP transport.
func setCORS(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Content-Type, "+sessionIDHeader+", Mcp-Protocol-Version")
	header.Set("Access-Control-Expose-Headers", sessionIDHeader)
}

// writeJSON encodes payload as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeJSONRPC encodes a JSON-RPC response. It exists as a named helper so the
// transport's intent is explicit at each call site.
func writeJSONRPC(w http.ResponseWriter, status int, resp jsonRPCResponse) {
	writeJSON(w, status, resp)
}
