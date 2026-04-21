// interp/tools.go
package interp

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
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
