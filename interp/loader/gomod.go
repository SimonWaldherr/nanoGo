// Package loader builds multi-file, multi-package nanoGo programs on top of
// interp.PackageScope: it walks a VFS tree, groups .go files by directory
// (one directory = one package), resolves imports against a module path and
// nanoGo's curated builtin allow-list, detects import cycles, and topo-sorts
// packages for correct init()/var evaluation order. It also hosts the
// data-driven test runner, benchmark runner, and hot-swap helpers.
//
// loader imports interp; interp never imports loader, so existing WASM
// consumers that only need Run/RunContext pay no size cost for any of this.
package loader

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// ParseModulePath extracts only the "module" line from go.mod data — no
// go.sum, no require/toolchain/replace directives, no version resolution.
// It is deliberately minimal: nanoGo's loader only needs the module path to
// map local import paths onto VFS directories.
func ParseModulePath(data []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("nanogo/loader: no module line found in go.mod")
}
