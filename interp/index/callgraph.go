package index

import (
	"go/ast"
	"strings"
)

// collectCallRefs walks d's body and records every call expression as a
// callRef, resolved later in buildCallGraph once every package's symbols are
// known. Only *ast.Ident and X.Sel forms (X itself a bare identifier) are
// recognized — chained or computed callees are not (they are rare in the Go
// subset nanoGo targets, and unresolvable without type information anyway).
func collectCallRefs(d *ast.FuncDecl) []callRef {
	if d.Body == nil {
		return nil
	}
	var refs []callRef
	ast.Inspect(d.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			refs = append(refs, callRef{Name: fn.Name})
		case *ast.SelectorExpr:
			if x, ok := fn.X.(*ast.Ident); ok {
				refs = append(refs, callRef{Qualifier: x.Name, Name: fn.Sel.Name})
			}
		}
		return true
	})
	return refs
}

// buildCallGraph resolves every collected callRef against the known
// FunctionEntry symbols and mutates entries in place: Calls/CalledBy for
// every match, and Tests on a production function whenever a same-package
// _test.go Test function calls it directly.
//
// Resolution is deliberately typeless and best-effort, matching the spec's
// own framing ("typloser Call-Graph via CallExpr-Matching gegen bekannte
// Symbole"): a bare call resolves within the caller's own package; a
// qualified call X.Sel resolves against package X if X names a known
// package, otherwise against any function/method named Sel that has a
// receiver (ambiguous when multiple types share a method name — not
// disambiguated here).
func buildCallGraph(entries []FunctionEntry, refs [][]callRef) {
	byPkgAndName := map[string]map[string][]int{}
	byMethodName := map[string][]int{}
	pkgExists := map[string]bool{}

	for i, e := range entries {
		pkgExists[e.Package] = true
		if byPkgAndName[e.Package] == nil {
			byPkgAndName[e.Package] = map[string][]int{}
		}
		byPkgAndName[e.Package][e.Name] = append(byPkgAndName[e.Package][e.Name], i)
		if e.Receiver != "" {
			byMethodName[e.Name] = append(byMethodName[e.Name], i)
		}
	}

	addEdge := func(callerIdx, calleeIdx int) {
		if callerIdx == calleeIdx {
			return
		}
		entries[callerIdx].Calls = appendUnique(entries[callerIdx].Calls, entries[calleeIdx].ID)
		entries[calleeIdx].CalledBy = appendUnique(entries[calleeIdx].CalledBy, entries[callerIdx].ID)
	}

	for i, callRefs := range refs {
		callerPkg := entries[i].Package
		for _, ref := range callRefs {
			var targets []int
			switch {
			case ref.Qualifier == "":
				targets = byPkgAndName[callerPkg][ref.Name]
			case pkgExists[ref.Qualifier]:
				targets = byPkgAndName[ref.Qualifier][ref.Name]
			default:
				targets = byMethodName[ref.Name]
			}
			for _, j := range targets {
				addEdge(i, j)
			}
		}
	}

	for i := range entries {
		if !isTestFile(entries[i].File) || !strings.HasPrefix(entries[i].Name, "Test") {
			continue
		}
		for _, calleeID := range entries[i].Calls {
			for j := range entries {
				if entries[j].ID == calleeID && !isTestFile(entries[j].File) {
					entries[j].Tests = appendUnique(entries[j].Tests, entries[i].Name)
				}
			}
		}
	}
}

func isTestFile(file string) bool {
	return strings.HasSuffix(file, "_test.go")
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
