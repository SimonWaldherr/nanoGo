package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestReusableBlockSetTracksFunctionLiteralDescendants(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "blocks.go", `package main
func main() {
		{ value := 1; _ = value }
		{ _ = func() int { return 1 } }
}`, 0)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var blocks []*ast.BlockStmt
	ast.Inspect(file, func(node ast.Node) bool {
		if block, ok := node.(*ast.BlockStmt); ok {
			blocks = append(blocks, block)
		}
		return true
	})
	if len(blocks) != 4 {
		t.Fatalf("found %d blocks, want 4", len(blocks))
	}
	reusable := buildReusableBlockSet(file)
	if !containsReusableBlock(reusable, blocks[1]) {
		t.Fatal("ordinary lexical block was not marked reusable")
	}
	if containsReusableBlock(reusable, blocks[0]) || containsReusableBlock(reusable, blocks[2]) {
		t.Fatal("block containing a function literal was marked reusable")
	}
}

func containsReusableBlock(blocks map[*ast.BlockStmt]struct{}, block *ast.BlockStmt) bool {
	_, ok := blocks[block]
	return ok
}
