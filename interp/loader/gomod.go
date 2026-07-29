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

// ModuleFile is the subset of go.mod that nanoGo can apply without a network
// module downloader. Replacements point module import prefixes at VFS roots;
// require versions are intentionally not resolved here because hosts provide
// dependency source explicitly through Options.DependencyRoots or replace.
type ModuleFile struct {
	Path     string
	Replaces map[string]string // module path -> local replacement path
}

// ParseModulePath extracts only the "module" line from go.mod data — no
// go.sum, no require/toolchain/replace directives, no version resolution.
// It is deliberately minimal: nanoGo's loader only needs the module path to
// map local import paths onto VFS directories.
func ParseModulePath(data []byte) (string, error) {
	mod, err := ParseModuleFile(data)
	if err != nil {
		return "", err
	}
	return mod.Path, nil
}

// ParseModuleFile reads a module declaration and local replace directives.
// It accepts both single-line and parenthesized replace blocks, ignores
// versions (which are irrelevant once source is already present in the VFS),
// and retains only local ./, ../, or absolute targets. Remote replacements
// remain the host's responsibility and can be supplied through
// Options.DependencyRoots instead.
func ParseModuleFile(data []byte) (ModuleFile, error) {
	mod := ModuleFile{Replaces: make(map[string]string)}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inReplaceBlock := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if inReplaceBlock {
			if line == ")" {
				inReplaceBlock = false
				continue
			}
			parseLocalReplace(line, mod.Replaces)
			continue
		}
		if line == "replace (" || line == "replace(" {
			inReplaceBlock = true
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			mod.Path = fields[1]
			continue
		}
		if strings.HasPrefix(line, "replace ") {
			parseLocalReplace(strings.TrimSpace(strings.TrimPrefix(line, "replace")), mod.Replaces)
		}
	}
	if err := scanner.Err(); err != nil {
		return ModuleFile{}, err
	}
	if mod.Path == "" {
		return ModuleFile{}, fmt.Errorf("nanogo/loader: no module line found in go.mod")
	}
	return mod, nil
}

func parseLocalReplace(line string, replaces map[string]string) {
	parts := strings.Split(line, "=>")
	if len(parts) != 2 {
		return
	}
	left, right := strings.Fields(strings.TrimSpace(parts[0])), strings.Fields(strings.TrimSpace(parts[1]))
	if len(left) == 0 || len(right) == 0 {
		return
	}
	target := right[0]
	if target == "." || strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") {
		replaces[left[0]] = target
	}
}
