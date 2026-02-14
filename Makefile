.PHONY: all build-wasm build-cli run-demo clean test

MODULE := simonwaldherr.de/go/nanogo

# Output directories
BUILD_DIR := build

all: build-wasm build-cli

# ---------- WASM target (for the web playground) ----------
WASM_OUT := web/nanogo.wasm

build-wasm:
	@mkdir -p $(dir $(WASM_OUT))
	GOOS=js GOARCH=wasm go build -o $(WASM_OUT) ./cmd/wasm

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
	go test ./...

# ---------- Housekeeping ----------
clean:
	rm -rf $(BUILD_DIR)
	rm -f $(WASM_OUT)

tidy:
	go mod tidy
	go vet ./...
