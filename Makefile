.PHONY: all build-wasm build-wasm-compressed build-cli build-mcp build-repl run-demo clean test tidy size-report benchmark

MODULE := simonwaldherr.de/go/nanogo

# Output directories
BUILD_DIR := build

all: build-wasm build-cli build-mcp

# ---------- MCP server (Model Context Protocol for LLMs) ----------
MCP_OUT := $(BUILD_DIR)/nanogo-mcp

build-mcp:
	@mkdir -p $(BUILD_DIR)
	go build -o $(MCP_OUT) ./cmd/mcp

# ---------- REPL ----------
build-repl:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/nanogo-repl ./cmd/repl

# ---------- WASM target (for the web playground) ----------
WASM_OUT := web/nanogo.wasm

build-wasm:
	@mkdir -p $(dir $(WASM_OUT))
	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o $(WASM_OUT) ./cmd/wasm

# Build the WASM and emit pre-compressed .gz/.br variants for static HTTP
# servers that support Content-Encoding negotiation. Brotli is optional;
# the target succeeds even if the `brotli` binary is not installed.
build-wasm-compressed: build-wasm
	@echo "--- gzip ---"
	@gzip -9 -k -f $(WASM_OUT)
	@if command -v brotli >/dev/null 2>&1; then \
		echo "--- brotli ---"; \
		brotli -f -q 11 -k $(WASM_OUT); \
	else \
		echo "(brotli not installed — skipping .br; install with 'apt-get install brotli')"; \
	fi
	@$(MAKE) --no-print-directory size-report

# ---------- Native CLI demo (safe interpreter) ----------
CLI_OUT := $(BUILD_DIR)/nanogo-cli

build-cli:
	@mkdir -p $(BUILD_DIR)
	go build -o $(CLI_OUT) ./cmd/cli

run-demo: build-cli
	@echo "--- running samples/features_demo.go ---"
	$(CLI_OUT) samples/features_demo.go

# ---------- Tests ----------
test:
	go test ./interp ./interp/loader ./interp/index ./cmd/mcp ./cmd/repl

# ---------- Benchmarks (informational; no -cpuprofile by default) ----------
benchmark: build-cli
	@echo "--- running samples/features_demo.go (timing) ---"
	@time $(CLI_OUT) samples/features_demo.go >/dev/null

# ---------- Size report for the WASM artifact ----------
# Prints uncompressed/gzip/brotli sizes so PRs can quote the delta.
size-report:
	@if [ ! -f $(WASM_OUT) ]; then \
		echo "$(WASM_OUT) not built — run 'make build-wasm' first."; exit 1; \
	fi
	@printf "%-20s %s\n" "uncompressed:" "$$(wc -c <$(WASM_OUT) | tr -d ' ') bytes ($$(du -h $(WASM_OUT) | cut -f1))"
	@if [ -f $(WASM_OUT).gz ]; then \
		printf "%-20s %s\n" "gzip (.gz):" "$$(wc -c <$(WASM_OUT).gz | tr -d ' ') bytes ($$(du -h $(WASM_OUT).gz | cut -f1))"; \
	else \
		gz=$$(gzip -9 -c $(WASM_OUT) | wc -c | tr -d ' '); \
		printf "%-20s %s\n" "gzip (in-memory):" "$$gz bytes"; \
	fi
	@if [ -f $(WASM_OUT).br ]; then \
		printf "%-20s %s\n" "brotli (.br):" "$$(wc -c <$(WASM_OUT).br | tr -d ' ') bytes ($$(du -h $(WASM_OUT).br | cut -f1))"; \
	elif command -v brotli >/dev/null 2>&1; then \
		br=$$(brotli -q 11 -c $(WASM_OUT) | wc -c | tr -d ' '); \
		printf "%-20s %s\n" "brotli (in-memory):" "$$br bytes"; \
	else \
		echo "brotli: not installed"; \
	fi

# ---------- Housekeeping ----------
clean:
	rm -rf $(BUILD_DIR)
	rm -f $(WASM_OUT) $(WASM_OUT).gz $(WASM_OUT).br

tidy:
	go mod tidy
	go vet ./...
