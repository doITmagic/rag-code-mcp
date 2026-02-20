package golang

import (
	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// CodeChunk represents a semantically meaningful piece of code
type CodeChunk struct {
	Type               string               `json:"type"`
	Name               string               `json:"name"`
	Package            string               `json:"package"`
	Language           string               `json:"language"`
	FilePath           string               `json:"file_path"`
	StartLine          int                  `json:"start_line"`
	EndLine            int                  `json:"end_line"`
	SelectionStartLine int                  `json:"selection_start_line,omitempty"`
	SelectionEndLine   int                  `json:"selection_end_line,omitempty"`
	Signature          string               `json:"signature"`
	Docstring          string               `json:"docstring"`
	Code               string               `json:"code"`
	Relations          []pkgParser.Relation `json:"relations,omitempty"`
	Metadata           map[string]any       `json:"metadata,omitempty"`
}

// PackageInfo contains comprehensive information about a Go package
type PackageInfo struct {
	Name        string         `json:"name"`
	Path        string         `json:"path"`
	Description string         `json:"description"`
	Functions   []FunctionInfo `json:"functions"`
	Types       []TypeInfo     `json:"types"`
	Constants   []ConstantInfo `json:"constants"`
	Variables   []VariableInfo `json:"variables"`
	Examples    []ExampleInfo  `json:"examples"`
	Imports     []string       `json:"imports"`
}

// FunctionInfo describes a function or method
type FunctionInfo struct {
	Name        string       `json:"name"`
	Signature   string       `json:"signature"`
	Description string       `json:"description"`
	Parameters  []ParamInfo  `json:"parameters"`
	Returns     []ReturnInfo `json:"returns"`
	Examples    []string     `json:"examples"`
	IsExported  bool         `json:"is_exported"`
	IsMethod    bool         `json:"is_method"`
	Receiver    string       `json:"receiver,omitempty"`
	FilePath    string       `json:"file_path,omitempty"`
	StartLine   int          `json:"start_line,omitempty"`
	EndLine     int          `json:"end_line,omitempty"`
	Code        string       `json:"code,omitempty"`
	Calls       []string     `json:"calls,omitempty"`
}

// TypeInfo describes a type declaration (struct, interface, alias, etc.)
type TypeInfo struct {
	Name        string       `json:"name"`
	Kind        string       `json:"kind"` // struct, interface, alias, etc.
	Description string       `json:"description"`
	Fields      []FieldInfo  `json:"fields,omitempty"`
	Methods     []MethodInfo `json:"methods,omitempty"`
	IsExported  bool         `json:"is_exported"`
	FilePath    string       `json:"file_path,omitempty"`
	StartLine   int          `json:"start_line,omitempty"`
	EndLine     int          `json:"end_line,omitempty"`
	Code        string       `json:"code,omitempty"`
}

// MethodInfo describes a method associated with a type
type MethodInfo struct {
	Name         string       `json:"name"`
	Signature    string       `json:"signature"`
	Description  string       `json:"description"`
	Parameters   []ParamInfo  `json:"parameters"`
	Returns      []ReturnInfo `json:"returns"`
	ReceiverType string       `json:"receiver_type"`
	IsExported   bool         `json:"is_exported"`
	FilePath     string       `json:"file_path,omitempty"`
	StartLine    int          `json:"start_line,omitempty"`
	EndLine      int          `json:"end_line,omitempty"`
	Code         string       `json:"code,omitempty"`
}

// ParamInfo describes a function or method parameter
type ParamInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ReturnInfo describes a function or method return value
type ReturnInfo struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// FieldInfo describes a struct field
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// ConstantInfo describes a constant declaration
type ConstantInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Description string `json:"description"`
	IsExported  bool   `json:"is_exported"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// VariableInfo describes a variable declaration
type VariableInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	IsExported  bool   `json:"is_exported"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// ExampleInfo describes a code example
type ExampleInfo struct {
	Name string `json:"name"`
	Code string `json:"code"`
	Doc  string `json:"doc"`
}
