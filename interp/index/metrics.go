package index

import "go/ast"

// computeMetricsWithLOC derives purely AST-based complexity indicators for
// d — no type information, no control-flow-graph construction, just node
// counting, matching this package's no-go/types constraint. startLine/endLine
// come from the caller's own fset.Position lookups on d.Pos()/d.End().
func computeMetricsWithLOC(d *ast.FuncDecl, startLine, endLine int) Metrics {
	m := computeMetrics(d)
	m.LOC = endLine - startLine + 1
	return m
}

func computeMetrics(d *ast.FuncDecl) Metrics {
	m := Metrics{CyclomaticComplexity: 1}
	if d.Body == nil {
		return m
	}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch nd := n.(type) {
		case *ast.IfStmt:
			m.CyclomaticComplexity++
		case *ast.ForStmt:
			m.CyclomaticComplexity++
		case *ast.RangeStmt:
			m.CyclomaticComplexity++
		case *ast.CaseClause:
			if len(nd.List) > 0 { // a bare "default:" doesn't add a branch
				m.CyclomaticComplexity++
			}
		case *ast.CommClause:
			if nd.Comm != nil { // a bare "default:" in a select doesn't add a branch
				m.CyclomaticComplexity++
			}
		case *ast.BinaryExpr:
			if nd.Op.String() == "&&" || nd.Op.String() == "||" {
				m.CyclomaticComplexity++
			}
		}
		return true
	})
	m.MaxNestingDepth = blockDepth(d.Body, 0)
	return m
}

// blockDepth walks the small set of block-containing statement kinds nanoGo
// actually supports, counting nesting depth explicitly (a plain ast.Inspect
// can't distinguish "entering" from "leaving" a block, so this recurses by
// concrete statement type instead). The function body's own outermost block
// counts as depth 1.
func blockDepth(s ast.Stmt, depth int) int {
	switch st := s.(type) {
	case *ast.BlockStmt:
		best := depth
		next := depth + 1
		if next > best {
			best = next
		}
		for _, inner := range st.List {
			if v := blockDepth(inner, next); v > best {
				best = v
			}
		}
		return best
	case *ast.IfStmt:
		best := depth
		if v := blockDepth(st.Body, depth); v > best {
			best = v
		}
		if st.Else != nil {
			if v := blockDepth(st.Else, depth); v > best {
				best = v
			}
		}
		return best
	case *ast.ForStmt:
		return blockDepth(st.Body, depth)
	case *ast.RangeStmt:
		return blockDepth(st.Body, depth)
	case *ast.SwitchStmt:
		return caseClauseDepth(st.Body, depth+1)
	case *ast.TypeSwitchStmt:
		return caseClauseDepth(st.Body, depth+1)
	case *ast.SelectStmt:
		next := depth + 1
		best := next // an empty select {} still costs one level, like an empty if/for/switch body
		for _, c := range st.Body.List {
			cc, ok := c.(*ast.CommClause)
			if !ok {
				continue
			}
			for _, inner := range cc.Body {
				if v := blockDepth(inner, next); v > best {
					best = v
				}
			}
		}
		return best
	default:
		return depth
	}
}

// caseClauseDepth scores a switch/type-switch's clause bodies at depth,
// which the caller has already incremented by one for the switch's own
// brace-delimited body — mirroring how blockDepth's BlockStmt case always
// adds a level before recursing into an if/for/range body, so a switch
// nested at the same syntactic position reports the same MaxNestingDepth an
// equivalent if/for/range would.
func caseClauseDepth(body *ast.BlockStmt, depth int) int {
	best := depth
	if body == nil {
		return best
	}
	for _, c := range body.List {
		cc, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, inner := range cc.Body {
			if v := blockDepth(inner, depth); v > best {
				best = v
			}
		}
	}
	return best
}
