package php

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// PackageInfo contains comprehensive information about a PHP namespace/package
// In PHP context, this represents a namespace or the global scope
type PackageInfo struct {
	Namespace   string          `json:"namespace"`   // Namespace name (e.g., "App\\Http\\Controllers")
	Path        string          `json:"path"`        // Directory path
	Description string          `json:"description"` // From file-level docblock
	Classes     []ClassInfo     `json:"classes"`
	Interfaces  []InterfaceInfo `json:"interfaces"`
	Traits      []TraitInfo     `json:"traits"`
	Functions   []FunctionInfo  `json:"functions"` // Global functions
	Constants   []ConstantInfo  `json:"constants"` // Global constants
	Uses        []string        `json:"uses"`      // Use imports

	// AST nodes for advanced analysis (not serialized to JSON)
	ClassNodes map[string]*ast.StmtClass `json:"-"` // Map: full class name -> AST node
}

// ClassInfo describes a PHP class
type ClassInfo struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	FullName    string            `json:"full_name"`   // Fully qualified name (FQN)
	Description string            `json:"description"` // PHPDoc description
	Extends     string            `json:"extends,omitempty"`
	Implements  []string          `json:"implements,omitempty"`
	Uses        []string          `json:"uses,omitempty"` // Trait usage
	Methods     []MethodInfo      `json:"methods"`
	Properties  []PropertyInfo    `json:"properties"`
	Constants   []ConstantInfo    `json:"constants"`
	IsAbstract  bool              `json:"is_abstract"`
	IsFinal     bool              `json:"is_final"`
	FilePath    string            `json:"file_path,omitempty"`
	StartLine   int               `json:"start_line,omitempty"`
	EndLine     int               `json:"end_line,omitempty"`
	Code        string            `json:"code,omitempty"`
	Imports     map[string]string `json:"imports,omitempty"` // Map of alias -> full name
}

// InterfaceInfo describes a PHP interface
type InterfaceInfo struct {
	Name        string         `json:"name"`
	Namespace   string         `json:"namespace"`
	FullName    string         `json:"full_name"`
	Description string         `json:"description"`
	Extends     []string       `json:"extends,omitempty"` // Interfaces can extend multiple
	Methods     []MethodInfo   `json:"methods"`
	Constants   []ConstantInfo `json:"constants"`
	FilePath    string         `json:"file_path,omitempty"`
	StartLine   int            `json:"start_line,omitempty"`
	EndLine     int            `json:"end_line,omitempty"`
	Code        string         `json:"code,omitempty"`
}

// TraitInfo describes a PHP trait
type TraitInfo struct {
	Name        string         `json:"name"`
	Namespace   string         `json:"namespace"`
	FullName    string         `json:"full_name"`
	Description string         `json:"description"`
	Methods     []MethodInfo   `json:"methods"`
	Properties  []PropertyInfo `json:"properties"`
	FilePath    string         `json:"file_path,omitempty"`
	StartLine   int            `json:"start_line,omitempty"`
	EndLine     int            `json:"end_line,omitempty"`
	Code        string         `json:"code,omitempty"`
}

// MethodInfo describes a class/interface/trait method
type MethodInfo struct {
	Name        string                 `json:"name"`
	Signature   string                 `json:"signature"`
	Description string                 `json:"description"`
	Parameters  []codetypes.ParamInfo  `json:"parameters"`
	ReturnType  string                 `json:"return_type,omitempty"`
	Returns     []codetypes.ReturnInfo `json:"returns,omitempty"`
	Visibility  string                 `json:"visibility"` // public, protected, private
	IsStatic    bool                   `json:"is_static"`
	IsAbstract  bool                   `json:"is_abstract"`
	IsFinal     bool                   `json:"is_final"`
	ClassName   string                 `json:"class_name,omitempty"` // Parent class/interface/trait
	FilePath    string                 `json:"file_path,omitempty"`
	StartLine   int                    `json:"start_line,omitempty"`
	EndLine     int                    `json:"end_line,omitempty"`
	Code        string                 `json:"code,omitempty"`
}

// FunctionInfo describes a global function or method
type FunctionInfo struct {
	Name        string                 `json:"name"`
	Signature   string                 `json:"signature"`
	Description string                 `json:"description"`
	Parameters  []codetypes.ParamInfo  `json:"parameters"`
	ReturnType  string                 `json:"return_type,omitempty"`
	Returns     []codetypes.ReturnInfo `json:"returns,omitempty"`
	Namespace   string                 `json:"namespace,omitempty"`
	IsMethod    bool                   `json:"is_method"`
	ClassName   string                 `json:"class_name,omitempty"` // If method
	Visibility  string                 `json:"visibility,omitempty"` // If method
	IsStatic    bool                   `json:"is_static,omitempty"`  // If method
	FilePath    string                 `json:"file_path,omitempty"`
	StartLine   int                    `json:"start_line,omitempty"`
	EndLine     int                    `json:"end_line,omitempty"`
	Code        string                 `json:"code,omitempty"`
}

// PropertyInfo describes a class/trait property
type PropertyInfo struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"` // Type hint if available
	DefaultValue string `json:"default_value,omitempty"`
	Description  string `json:"description"`
	Visibility   string `json:"visibility"` // public, protected, private
	IsStatic     bool   `json:"is_static"`
	IsReadonly   bool   `json:"is_readonly"` // PHP 8.1+
	FilePath     string `json:"file_path,omitempty"`
	StartLine    int    `json:"start_line,omitempty"`
	EndLine      int    `json:"end_line,omitempty"`
}

// ConstantInfo describes a class/interface constant or global constant
type ConstantInfo struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description"`
	Visibility  string `json:"visibility,omitempty"` // For class constants (PHP 7.1+)
	ClassName   string `json:"class_name,omitempty"` // If class constant
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}
