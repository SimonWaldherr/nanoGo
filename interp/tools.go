// interp/tools.go
package interp

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"time"
)

// FormatSource formats Go source code using the standard go/format package.
// It returns the formatted source, or (original, error) if the source cannot be parsed.
func FormatSource(src string) (string, error) {
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return src, err
	}
	return string(formatted), nil
}

// VetIssue describes a potential problem found by VetSource.
type VetIssue struct {
	Line    int
	Column  int
	Message string
}

func (v VetIssue) String() string {
	return fmt.Sprintf("%d:%d: %s", v.Line, v.Column, v.Message)
}

// VetSource performs basic static analysis on the source code and returns
// a list of potential issues. It covers a curated subset of checks similar
// to 'go vet': unreachable code, printf argument count mismatches, and
// self-assignments.
func VetSource(src string) ([]VetIssue, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "input.go", src, parser.AllErrors)
	if err != nil {
		return nil, err
	}

	var issues []VetIssue

	// Check 1: unreachable code after a terminating statement in a block.
	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i, stmt := range block.List {
			if isTerminatingStmt(stmt) && i < len(block.List)-1 {
				pos := fset.Position(block.List[i+1].Pos())
				issues = append(issues, VetIssue{
					Line:    pos.Line,
					Column:  pos.Column,
					Message: "unreachable code",
				})
				break // only report the first unreachable statement per block
			}
		}
		return true
	})

	// Check 2: printf-family format verb / argument count mismatch.
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callFuncName(call)
		if !isPrintfVariant(name) || len(call.Args) < 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		fmtStr := strings.Trim(lit.Value, `"`)
		expected := countPrintfVerbs(fmtStr)
		got := len(call.Args) - 1
		if expected != got {
			pos := fset.Position(call.Pos())
			issues = append(issues, VetIssue{
				Line:    pos.Line,
				Column:  pos.Column,
				Message: fmt.Sprintf("%s: format has %d verb(s) but %d argument(s) given", name, expected, got),
			})
		}
		return true
	})

	// Check 3: self-assignment (x = x has no effect).
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN {
			return true
		}
		for i := range assign.Lhs {
			if i >= len(assign.Rhs) {
				break
			}
			lIdent, lOK := assign.Lhs[i].(*ast.Ident)
			rIdent, rOK := assign.Rhs[i].(*ast.Ident)
			if lOK && rOK && lIdent.Name == rIdent.Name && lIdent.Name != "_" {
				pos := fset.Position(assign.Pos())
				issues = append(issues, VetIssue{
					Line:    pos.Line,
					Column:  pos.Column,
					Message: fmt.Sprintf("self-assignment: %s = %s has no effect", lIdent.Name, rIdent.Name),
				})
			}
		}
		return true
	})

	return issues, nil
}

// AstNode is one node of the serializable syntax tree produced by
// InspectSource. Children preserve source order; Line/Col are 1-based.
type AstNode struct {
	Type     string     `json:"type"`
	Label    string     `json:"label,omitempty"`
	Line     int        `json:"line,omitempty"`
	Col      int        `json:"col,omitempty"`
	EndLine  int        `json:"endLine,omitempty"`
	Children []*AstNode `json:"children,omitempty"`
}

// InspectResult bundles the AST with cheap structural statistics so a host
// UI can show "what did the parser see" without re-walking the tree.
type InspectResult struct {
	Tree      *AstNode `json:"tree"`
	NodeCount int      `json:"nodeCount"`
	MaxDepth  int      `json:"maxDepth"`
	Funcs     []string `json:"funcs"`
	Imports   []string `json:"imports"`
	ParseUs   int64    `json:"parseUs"`
}

// InspectSource parses src (including comments) and returns its syntax tree
// in a compact, JSON-friendly form together with structural statistics. It
// never executes any code.
func InspectSource(src string) (*InspectResult, error) {
	fset := token.NewFileSet()
	start := time.Now()
	file, err := parser.ParseFile(fset, "input.go", src, parser.ParseComments)
	parseUs := time.Since(start).Microseconds()
	if err != nil {
		return nil, err
	}

	result := &InspectResult{ParseUs: parseUs}
	var stack []*AstNode
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		node := &AstNode{Type: astTypeName(n), Label: astNodeLabel(n)}
		if pos := n.Pos(); pos.IsValid() {
			p := fset.Position(pos)
			node.Line, node.Col = p.Line, p.Column
		}
		if end := n.End(); end.IsValid() {
			node.EndLine = fset.Position(end).Line
		}
		result.NodeCount++
		if depth := len(stack) + 1; depth > result.MaxDepth {
			result.MaxDepth = depth
		}
		if len(stack) == 0 {
			result.Tree = node
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, node)
		}
		stack = append(stack, node)

		switch d := n.(type) {
		case *ast.FuncDecl:
			result.Funcs = append(result.Funcs, d.Name.Name)
		case *ast.ImportSpec:
			result.Imports = append(result.Imports, strings.Trim(d.Path.Value, `"`))
		}
		return true
	})
	return result, nil
}

// astTypeName reports the node's concrete type without the "*ast." prefix.
func astTypeName(n ast.Node) string {
	name := fmt.Sprintf("%T", n)
	name = strings.TrimPrefix(name, "*ast.")
	return strings.TrimPrefix(name, "ast.")
}

// astNodeLabel extracts the human-meaningful token of a node (identifier
// names, literal values, operators) for one-line tree rendering.
func astNodeLabel(n ast.Node) string {
	switch v := n.(type) {
	case *ast.File:
		return "package " + v.Name.Name
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.FuncDecl:
		return v.Name.Name
	case *ast.ImportSpec:
		return v.Path.Value
	case *ast.BinaryExpr:
		return v.Op.String()
	case *ast.UnaryExpr:
		return v.Op.String()
	case *ast.AssignStmt:
		return v.Tok.String()
	case *ast.IncDecStmt:
		return v.Tok.String()
	case *ast.BranchStmt:
		return v.Tok.String()
	case *ast.GenDecl:
		return v.Tok.String()
	case *ast.CallExpr:
		return callFuncName(v)
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.RangeStmt:
		return "range"
	case *ast.TypeSpec:
		return v.Name.Name
	case *ast.Comment:
		return v.Text
	}
	return ""
}

// CallGraphCall is one call site found inside a function's body.
type CallGraphCall struct {
	Name     string `json:"name"` // callee exactly as written: "fmt.Println", "t.Foo", "Bar"
	Line     int    `json:"line"`
	Resolved string `json:"resolved,omitempty"` // display name of the matching local func/method, if any
}

// CallGraphFunc is one top-level function or method declaration together
// with the calls it makes and (for calls resolved to another local
// declaration) the reverse edge of who calls it.
type CallGraphFunc struct {
	Name       string          `json:"name"` // "Foo", or "Type.Method" for a method
	Recv       string          `json:"recv,omitempty"`
	Line       int             `json:"line"`
	Calls      []CallGraphCall `json:"calls,omitempty"`
	CalledBy   []string        `json:"calledBy,omitempty"`
	Complexity int             `json:"complexity"`
	LOC        int             `json:"loc"`
	Recursive  bool            `json:"recursive,omitempty"`
}

// CallGraphResult is the full per-file call graph produced by
// AnalyzeCallGraph, in source declaration order.
type CallGraphResult struct {
	Funcs []CallGraphFunc `json:"funcs"`
}

// recvTypeName returns a method's receiver type name (unwrapping a pointer
// receiver), or "" for a plain function declaration.
func recvTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	expr := fd.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// AnalyzeCallGraph parses src and reports, for every top-level function and
// method, which functions it calls and (for calls resolved to another local
// declaration) which local functions call it back. It never executes any
// code — this is a purely static, syntactic analysis.
//
// Resolution is intentionally best-effort rather than fully type-checked:
// a plain call "Foo(...)" resolves exactly against a declared function
// named "Foo"; a selector call "x.Foo(...)" resolves against a declared
// method "Foo" only when exactly one declared receiver type has a method
// by that name (an ambiguous or externally-defined callee — fmt.Println,
// a call through an interface, etc. — is still listed under Calls, just
// without a Resolved target and therefore without a CalledBy edge).
func AnalyzeCallGraph(src string) (*CallGraphResult, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "input.go", src, 0)
	if err != nil {
		return nil, err
	}

	type declInfo struct {
		decl *ast.FuncDecl
		key  string
		recv string
	}
	var decls []declInfo
	methodOwners := map[string][]string{}

	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		recv := recvTypeName(fd)
		key := fd.Name.Name
		if recv != "" {
			key = recv + "." + fd.Name.Name
			methodOwners[fd.Name.Name] = append(methodOwners[fd.Name.Name], recv)
		}
		decls = append(decls, declInfo{decl: fd, key: key, recv: recv})
	}

	// Funcs is allocated up front at its final length so byKey's pointers
	// into it stay valid: appending to the slice afterward could reallocate
	// the backing array and silently orphan every pointer taken before that.
	result := &CallGraphResult{Funcs: make([]CallGraphFunc, len(decls))}
	byKey := make(map[string]*CallGraphFunc, len(decls))
	for i, di := range decls {
		pos := fset.Position(di.decl.Pos())
		endPos := fset.Position(di.decl.End())
		complexity, loc := funcComplexityAndLOC(di.decl, pos.Line, endPos.Line)
		result.Funcs[i] = CallGraphFunc{Name: di.key, Recv: di.recv, Line: pos.Line, Complexity: complexity, LOC: loc}
		byKey[di.key] = &result.Funcs[i]
	}

	for i, di := range decls {
		if di.decl.Body == nil {
			continue
		}
		ast.Inspect(di.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callFuncName(call)
			if name == "" {
				return true
			}
			pos := fset.Position(call.Pos())
			cc := CallGraphCall{Name: name, Line: pos.Line}

			if _, ok := byKey[name]; ok {
				cc.Resolved = name
			} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if owners := methodOwners[sel.Sel.Name]; len(owners) == 1 {
					cc.Resolved = owners[0] + "." + sel.Sel.Name
				}
			}
			result.Funcs[i].Calls = append(result.Funcs[i].Calls, cc)
			return true
		})
		for _, call := range result.Funcs[i].Calls {
			if call.Resolved == di.key {
				result.Funcs[i].Recursive = true
				break
			}
		}
	}

	for _, cf := range result.Funcs {
		for _, call := range cf.Calls {
			if call.Resolved == "" {
				continue
			}
			if target, ok := byKey[call.Resolved]; ok {
				target.CalledBy = append(target.CalledBy, cf.Name)
			}
		}
	}

	return result, nil
}

// funcComplexityAndLOC computes a purely AST-based cyclomatic complexity
// (McCabe, starting at 1) plus a line count for d, using only branch-adding
// node kinds — no go/types, matching AnalyzeCallGraph's single-file, no-import-
// resolution scope. This intentionally mirrors interp/index/metrics.go's
// computeMetrics, which can't be imported here directly: that package imports
// interp for shared VFS helpers, so interp importing it back would cycle.
func funcComplexityAndLOC(fd *ast.FuncDecl, startLine, endLine int) (complexity, loc int) {
	complexity = 1
	if fd.Body != nil {
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch nd := n.(type) {
			case *ast.IfStmt:
				complexity++
			case *ast.ForStmt:
				complexity++
			case *ast.RangeStmt:
				complexity++
			case *ast.CaseClause:
				if len(nd.List) > 0 { // a bare "default:" doesn't add a branch
					complexity++
				}
			case *ast.CommClause:
				if nd.Comm != nil { // a bare "default:" in a select doesn't add a branch
					complexity++
				}
			case *ast.BinaryExpr:
				if nd.Op.String() == "&&" || nd.Op.String() == "||" {
					complexity++
				}
			}
			return true
		})
	}
	return complexity, endLine - startLine + 1
}

// isTerminatingStmt returns true if stmt unconditionally transfers control.
func isTerminatingStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.BREAK || s.Tok == token.CONTINUE || s.Tok == token.GOTO
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
		}
	}
	return false
}

// callFuncName returns the qualified function name from a call expression.
func callFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
	}
	return ""
}

var printfVariants = map[string]bool{
	"fmt.Printf":  true,
	"fmt.Sprintf": true,
	"fmt.Fprintf": true,
	"fmt.Errorf":  true,
}

func isPrintfVariant(name string) bool { return printfVariants[name] }

// countPrintfVerbs counts the number of non-%% format verbs in a format string.
// It properly handles flags, width, precision, and verb characters so that
// specifiers like %5d, %.2f, and %-10s are each counted as a single verb.
func countPrintfVerbs(format string) int {
	count := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++ // advance past '%'
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			continue // %% is a literal percent, not a verb
		}
		// Skip optional flags: +, -, #, space, 0
		for i < len(format) && strings.ContainsRune("+-# 0", rune(format[i])) {
			i++
		}
		// Skip optional width digits
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			i++
		}
		// Skip optional precision (.digits)
		if i < len(format) && format[i] == '.' {
			i++
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		// Whatever remains is the verb character itself.
		if i < len(format) {
			count++
		}
	}
	return count
}
