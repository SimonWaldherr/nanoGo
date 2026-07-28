// http_server demonstrates running nanoGo inside a net/http handler to
// execute untrusted guest snippets per-request: a fresh interpreter and
// output buffer per request, a request-scoped timeout, deterministic step
// limits, and safe error-to-HTTP-status mapping. Run it with:
// go run ./examples/http_server
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// maxSnippetBytes bounds how much guest source we will read from a request
// body, so a client cannot exhaust host memory before we even run anything.
const maxSnippetBytes = 64 * 1024

// runSnippet compiles and runs the request body as a nanoGo guest program,
// with a fresh, isolated interpreter for every request.
func runSnippet(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSnippetBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// A brand new interpreter per request: no state, natives, or output
	// buffer is ever shared across requests.
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)

	var output strings.Builder
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			output.WriteString(interp.ToString(args[0]))
			output.WriteString("\n")
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := interp.ToString(args[0])
		return fmt.Sprintf(format, args[1:]...), nil
	})

	// Deterministic limits stop a runaway or malicious snippet even before
	// the timeout below fires.
	vm.Limits = interp.ExecutionLimits{MaxSteps: 2_000_000, MaxGoroutines: 16}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := vm.RunContext(ctx, string(body)); err != nil {
		// Guest-code failure (bad syntax, a hit limit, a runtime panic) is
		// the client's fault, not the server's: respond 422, not 500.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, output.String())
}

func main() {
	srv := httptest.NewServer(http.HandlerFunc(runSnippet))
	defer srv.Close()

	fmt.Println("--- valid snippet ---")
	validSnippet := `package main
func main() {
	for i := 0; i < 3; i++ {
		fmt.Printf("line %d\n", i)
	}
}`
	resp, err := http.Post(srv.URL, "text/plain", strings.NewReader(validSnippet))
	if err != nil {
		log.Fatal(err)
	}
	validBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("status:", resp.StatusCode)
	fmt.Println("body:")
	fmt.Print(string(validBody))

	fmt.Println("--- runaway snippet ---")
	runawaySnippet := `package main
func main() {
	for {
	}
}`
	resp, err = http.Post(srv.URL, "text/plain", strings.NewReader(runawaySnippet))
	if err != nil {
		log.Fatal(err)
	}
	runawayBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("status:", resp.StatusCode)
	fmt.Println("body:", strings.TrimSpace(string(runawayBody)))
}
