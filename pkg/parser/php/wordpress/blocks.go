package wordpress

import (
	"github.com/VKCOM/php-parser/pkg/ast"
)

// BlockAnalyzer extracts Gutenberg block and block pattern registrations
type BlockAnalyzer struct {
	astHelper *ASTHelper
}

// NewBlockAnalyzer creates a new block analyzer
func NewBlockAnalyzer() *BlockAnalyzer {
	return &BlockAnalyzer{
		astHelper: NewASTHelper(),
	}
}

// AnalyzeBlocks detects register_block_type calls from AST
func (a *BlockAnalyzer) AnalyzeBlocks(root ast.Vertex, filePath string) []Block {
	var blocks []Block
	a.walkForBlocks(root, filePath, &blocks)
	return blocks
}

// AnalyzeBlockPatterns detects register_block_pattern calls from AST
func (a *BlockAnalyzer) AnalyzeBlockPatterns(root ast.Vertex, filePath string) []BlockPattern {
	var patterns []BlockPattern
	a.walkForBlockPatterns(root, filePath, &patterns)
	return patterns
}

func (a *BlockAnalyzer) walkForBlocks(node ast.Vertex, filePath string, blocks *[]Block) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForBlocks(stmt, filePath, blocks)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForBlocks(stmt, filePath, blocks)
		}
	case *ast.StmtExpression:
		a.walkForBlocks(n.Expr, filePath, blocks)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForBlocks(stmt, filePath, blocks)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForBlocks(s, filePath, blocks)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForBlocks(stmt, filePath, blocks)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForBlocks(n.Stmt, filePath, blocks)
		}
	case *ast.ExprFunctionCall:
		funcName := a.astHelper.extractFunctionName(n.Function)
		if funcName == "register_block_type" {
			args := a.astHelper.extractCallArgs(n.Args)
			if len(args) > 0 {
				*blocks = append(*blocks, Block{
					Name:      args[0],
					FilePath:  filePath,
					StartLine: n.Position.StartLine,
					EndLine:   n.Position.EndLine,
				})
			}
		}
	}
}

func (a *BlockAnalyzer) walkForBlockPatterns(node ast.Vertex, filePath string, patterns *[]BlockPattern) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForBlockPatterns(stmt, filePath, patterns)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForBlockPatterns(stmt, filePath, patterns)
		}
	case *ast.StmtExpression:
		a.walkForBlockPatterns(n.Expr, filePath, patterns)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForBlockPatterns(stmt, filePath, patterns)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForBlockPatterns(s, filePath, patterns)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForBlockPatterns(stmt, filePath, patterns)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForBlockPatterns(n.Stmt, filePath, patterns)
		}
	case *ast.ExprFunctionCall:
		funcName := a.astHelper.extractFunctionName(n.Function)
		if funcName == "register_block_pattern" {
			args := a.astHelper.extractCallArgs(n.Args)
			if len(args) > 0 {
				*patterns = append(*patterns, BlockPattern{
					Name:      args[0],
					FilePath:  filePath,
					StartLine: n.Position.StartLine,
					EndLine:   n.Position.EndLine,
				})
			}
		}
	}
}
