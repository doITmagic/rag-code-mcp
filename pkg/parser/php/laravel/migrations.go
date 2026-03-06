package laravel

import (
	"fmt"
	"os"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
)

// MigrationAnalyzer parses Laravel migration files
type MigrationAnalyzer struct {
	astHelper *ASTPropertyExtractor
}

// NewMigrationAnalyzer creates a new migration analyzer
func NewMigrationAnalyzer() *MigrationAnalyzer {
	return &MigrationAnalyzer{
		astHelper: NewASTPropertyExtractor(),
	}
}

// Analyze parses the given migration files and returns extracted migrations
func (ma *MigrationAnalyzer) Analyze(filePaths []string) ([]Migration, error) {
	var allMigrations []Migration

	for _, path := range filePaths {
		migrations, err := ma.analyzeFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error analyzing migration file %s: %v\n", path, err)
			continue
		}
		allMigrations = append(allMigrations, migrations...)
	}

	return allMigrations, nil
}

func (ma *MigrationAnalyzer) analyzeFile(filePath string) ([]Migration, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var parserErrors []*errors.Error
	rootNode, err := parser.Parse(content, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			parserErrors = append(parserErrors, e)
		},
	})

	if err != nil {
		return nil, err
	}

	collector := &migrationCollector{
		migrations: []Migration{},
		filePath:   filePath,
		astHelper:  ma.astHelper,
	}

	traverser.NewTraverser(collector).Traverse(rootNode)

	return collector.migrations, nil
}

type migrationCollector struct {
	visitor.Null
	migrations []Migration
	filePath   string
	astHelper  *ASTPropertyExtractor
}

func (v *migrationCollector) StmtClass(node *ast.StmtClass) {
	if v.isMigration(node.Extends) {
		className := ""
		if node.Name != nil {
			if ident, ok := node.Name.(*ast.Identifier); ok {
				className = string(ident.Value)
			}
		} else {
			className = "AnonymousMigration"
		}

		migration := Migration{
			ClassName:   className,
			FilePath:    v.filePath,
			StartLine:   node.Position.StartLine,
			EndLine:     node.Position.EndLine,
			Description: v.extractTableNameFromFileName(v.filePath), // Simple fallback
		}

		v.extractSchemaOperations(node, &migration)
		v.migrations = append(v.migrations, migration)
	}
}

func (v *migrationCollector) isMigration(extends ast.Vertex) bool {
	if extends == nil {
		return false
	}
	name := v.extractName(extends)
	return name == "Migration" || strings.HasSuffix(name, "\\Migration")
}

func (v *migrationCollector) extractName(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	switch n := node.(type) {
	case *ast.Name:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	case *ast.NameFullyQualified:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return "\\" + strings.Join(parts, "\\")
	}
	return ""
}

func (v *migrationCollector) extractTableNameFromFileName(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	parts := strings.Split(base, "_")
	if len(parts) > 4 {
		// e.g., 2014_10_12_000000_create_users_table.php
		name := strings.Join(parts[4:], "_")
		name = strings.TrimSuffix(name, ".php")
		return name
	}
	return base
}

func (v *migrationCollector) extractSchemaOperations(classNode *ast.StmtClass, migration *Migration) {
	for _, stmt := range classNode.Stmts {
		if methodNode, ok := stmt.(*ast.StmtClassMethod); ok {
			methodName := ""
			if ident, ok := methodNode.Name.(*ast.Identifier); ok {
				methodName = string(ident.Value)
			}
			if methodName == "up" {
				v.walkForSchemaCalls(methodNode.Stmt, migration)
			}
		}
	}
}

func (v *migrationCollector) walkForSchemaCalls(stmt ast.Vertex, migration *Migration) {
	if stmt == nil {
		return
	}

	switch node := stmt.(type) {
	case *ast.StmtStmtList:
		for _, s := range node.Stmts {
			v.walkForSchemaCalls(s, migration)
		}
	case *ast.StmtExpression:
		if node.Expr != nil {
			v.walkForSchemaCalls(node.Expr, migration)
		}
	case *ast.ExprStaticCall:
		className := v.extractName(node.Class)
		if className == "Schema" || strings.HasSuffix(className, "\\Schema") {
			methodName := ""
			if ident, ok := node.Call.(*ast.Identifier); ok {
				methodName = string(ident.Value)
			}
			if methodName == "create" || methodName == "table" || methodName == "dropIfExists" || methodName == "drop" {
				migration.Operations = append(migration.Operations, methodName)
				if len(node.Args) > 0 {
					if arg, ok := node.Args[0].(*ast.Argument); ok {
						table := v.astHelper.extractStringFromExpr(arg.Expr)
						if table != "" {
							migration.Table = table
						}
					}
				}
			}
		}
	}
}
