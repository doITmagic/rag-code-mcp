package golang

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/doc"
	"go/token"
	"go/types"
	"os"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

func (a *Analyzer) extractSymbolsFromDoc(fset *token.FileSet, docPkg *doc.Package, files []*ast.File) []parser.Symbol {
var symbols []parser.Symbol

// Build a map for AST bodies (doc.New clears bodies)
bodyMap := a.buildBodyMap(files)

// Functions
for _, fn := range docPkg.Funcs {
symbols = append(symbols, a.mapFunc(fset, fn, bodyMap, docPkg.Name, ""))
}

// Types and their methods
for _, t := range docPkg.Types {
symbols = append(symbols, a.mapType(fset, t, docPkg.Name))
for _, m := range t.Methods {
symbols = append(symbols, a.mapFunc(fset, m, bodyMap, docPkg.Name, t.Name))
}
}

// Constants
for _, v := range docPkg.Consts {
symbols = append(symbols, a.mapValues(fset, v, docPkg.Name, parser.Const, "const")...)
}

// Variables
for _, v := range docPkg.Vars {
symbols = append(symbols, a.mapValues(fset, v, docPkg.Name, parser.Var, "var")...)
}

return symbols
}

func (a *Analyzer) buildBodyMap(files []*ast.File) map[string]*ast.BlockStmt {
m := make(map[string]*ast.BlockStmt)
for _, f := range files {
ast.Inspect(f, func(n ast.Node) bool {
if fn, ok := n.(*ast.FuncDecl); ok && fn.Body != nil {
key := fn.Name.Name
if fn.Recv != nil && len(fn.Recv.List) > 0 {
recvType := a.getReceiverTypeName(fn.Recv.List[0].Type)
if recvType != "" {
key = recvType + "." + key
}
}
m[key] = fn.Body
}
return true
})
}
return m
}

func (a *Analyzer) getReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func (a *Analyzer) mapFunc(fset *token.FileSet, fn *doc.Func, bodyMap map[string]*ast.BlockStmt, pkgName, receiver string) parser.Symbol {
key := fn.Name
if receiver != "" {
key = receiver + "." + key
}

start := fset.Position(fn.Decl.Pos())
end := fset.Position(fn.Decl.End())

if body := bodyMap[key]; body != nil {
end = fset.Position(body.End())
}

sym := parser.Symbol{
Name:      fn.Name,
Type:      parser.Function,
Package:   pkgName,
Docstring: strings.TrimSpace(fn.Doc),
StartLine: start.Line,
EndLine:   end.Line,
FilePath:  start.Filename,
Language:  "go",
Metadata:  make(map[string]any),
}

if receiver != "" {
sym.Type = parser.Method
sym.Metadata["receiver"] = receiver
}

if fn.Decl.Type != nil {
sym.Signature = a.getFunctionSignature(fn.Decl)
}

code, _ := a.readLines(start.Filename, start.Line, end.Line)
sym.Content = code

return sym
}

func (a *Analyzer) mapType(fset *token.FileSet, t *doc.Type, pkgName string) parser.Symbol {
start := fset.Position(t.Decl.Pos())
end := fset.Position(t.Decl.End())

sym := parser.Symbol{
Name:      t.Name,
Type:      parser.Type,
Package:   pkgName,
Docstring: strings.TrimSpace(t.Doc),
StartLine: start.Line,
EndLine:   end.Line,
FilePath:  start.Filename,
Language:  "go",
Metadata:  make(map[string]any),
}

for _, spec := range t.Decl.Specs {
if ts, ok := spec.(*ast.TypeSpec); ok {
kind := a.getTypeKind(ts.Type)
sym.Metadata["kind"] = kind
if kind == "interface" {
sym.Type = parser.Interface
}
sym.Signature = fmt.Sprintf("type %s %s", t.Name, kind)
}
}

code, _ := a.readLines(start.Filename, start.Line, end.Line)
sym.Content = code

return sym
}

func (a *Analyzer) mapValues(fset *token.FileSet, v *doc.Value, pkgName string, st parser.SymbolType, prefix string) []parser.Symbol {
var symbols []parser.Symbol
start := fset.Position(v.Decl.Pos())
end := fset.Position(v.Decl.End())

for _, spec := range v.Decl.Specs {
if vs, ok := spec.(*ast.ValueSpec); ok {
for i, name := range vs.Names {
sym := parser.Symbol{
Name:      name.Name,
Type:      st,
Package:   pkgName,
Docstring: strings.TrimSpace(v.Doc),
StartLine: start.Line,
EndLine:   end.Line,
FilePath:  start.Filename,
Language:  "go",
}

typeName := "unknown"
if vs.Type != nil {
typeName = a.typeToString(vs.Type)
}
sym.Signature = fmt.Sprintf("%s %s %s", prefix, name.Name, typeName)

if i < len(vs.Values) && vs.Values[i] != nil {
sym.Content = a.exprToString(vs.Values[i])
} else {
code, _ := a.readLines(start.Filename, start.Line, end.Line)
sym.Content = code
}
symbols = append(symbols, sym)
}
}
}
return symbols
}

func (a *Analyzer) getFunctionSignature(decl *ast.FuncDecl) string {
var parts []string
parts = append(parts, "func")
if decl.Recv != nil {
recv := a.fieldListToString(decl.Recv)
parts = append(parts, fmt.Sprintf("(%s)", recv))
}
parts = append(parts, decl.Name.Name)
if decl.Type.Params != nil {
params := a.fieldListToString(decl.Type.Params)
parts = append(parts, fmt.Sprintf("(%s)", params))
} else {
parts = append(parts, "()")
}
if decl.Type.Results != nil {
results := a.fieldListToString(decl.Type.Results)
if len(decl.Type.Results.List) == 1 && len(decl.Type.Results.List[0].Names) == 0 {
parts = append(parts, results)
} else {
parts = append(parts, fmt.Sprintf("(%s)", results))
}
}
return strings.Join(parts, " ")
}

func (a *Analyzer) fieldListToString(fields *ast.FieldList) string {
if fields == nil {
return ""
}
var parts []string
for _, field := range fields.List {
fieldType := a.typeToString(field.Type)
if len(field.Names) == 0 {
parts = append(parts, fieldType)
} else {
for _, name := range field.Names {
parts = append(parts, fmt.Sprintf("%s %s", name.Name, fieldType))
}
}
}
return strings.Join(parts, ", ")
}

func (a *Analyzer) typeToString(expr ast.Expr) string {
return types.ExprString(expr)
}

func (a *Analyzer) exprToString(expr ast.Expr) string {
switch e := expr.(type) {
case *ast.BasicLit:
return e.Value
case *ast.Ident:
return e.Name
default:
return "..."
}
}

func (a *Analyzer) getTypeKind(expr ast.Expr) string {
switch expr.(type) {
case *ast.StructType:
return "struct"
case *ast.InterfaceType:
return "interface"
case *ast.ArrayType:
return "array"
case *ast.MapType:
return "map"
case *ast.ChanType:
return "channel"
case *ast.FuncType:
return "function"
default:
return "alias"
}
}

func (a *Analyzer) readLines(path string, start, end int) (string, error) {
f, err := os.Open(path)
if err != nil {
return "", err
}
defer f.Close()

var lines []string
scanner := bufio.NewScanner(f)
line := 1
for scanner.Scan() {
if line >= start && line <= end {
lines = append(lines, scanner.Text())
}
if line > end {
break
}
line++
}
return strings.Join(lines, "\n"), nil
}

func (a *Analyzer) extractSymbolsFromAST(fset *token.FileSet, files []*ast.File) []parser.Symbol {
var symbols []parser.Symbol
for _, f := range files {
fileName := fset.Position(f.Pos()).Filename
ast.Inspect(f, func(n ast.Node) bool {
switch x := n.(type) {
case *ast.FuncDecl:
start := fset.Position(x.Pos())
end := fset.Position(x.End())
sym := parser.Symbol{
Name:      x.Name.Name,
Type:      parser.Function,
Package:   f.Name.Name,
StartLine: start.Line,
EndLine:   end.Line,
FilePath:  fileName,
Language:  "go",
Signature: a.getFunctionSignature(x),
Metadata:  make(map[string]any),
}
if x.Recv != nil {
sym.Type = parser.Method
sym.Metadata["receiver"] = a.fieldListToString(x.Recv)
}
if x.Doc != nil {
sym.Docstring = strings.TrimSpace(x.Doc.Text())
}
code, _ := a.readLines(fileName, start.Line, end.Line)
sym.Content = code
symbols = append(symbols, sym)

case *ast.GenDecl:
for _, spec := range x.Specs {
switch s := spec.(type) {
case *ast.TypeSpec:
start := fset.Position(s.Pos())
end := fset.Position(s.End())
kind := a.getTypeKind(s.Type)
sym := parser.Symbol{
Name:      s.Name.Name,
Type:      parser.Type,
Package:   f.Name.Name,
StartLine: start.Line,
EndLine:   end.Line,
FilePath:  fileName,
Language:  "go",
Signature: fmt.Sprintf("type %s %s", s.Name.Name, kind),
Metadata:  make(map[string]any),
}
sym.Metadata["kind"] = kind
if kind == "interface" {
sym.Type = parser.Interface
}
if x.Doc != nil {
sym.Docstring = strings.TrimSpace(x.Doc.Text())
}
code, _ := a.readLines(fileName, start.Line, end.Line)
sym.Content = code
symbols = append(symbols, sym)
}
}
}
return true
})
}
return symbols
}
