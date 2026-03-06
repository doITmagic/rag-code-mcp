package javascript

// JSInfo contains all extracted JavaScript/TypeScript information
type JSInfo struct {
	Functions  []JSFunction  `json:"functions,omitempty"`
	Classes    []JSClass     `json:"classes,omitempty"`
	Interfaces []TSInterface `json:"interfaces,omitempty"`
	Types      []TSTypeAlias `json:"types,omitempty"`
	Enums      []TSEnum      `json:"enums,omitempty"`
	Imports    []JSImport    `json:"imports,omitempty"`
	Exports    []JSExport    `json:"exports,omitempty"`
}

// JSFunction represents a JS/TS function (classic or arrow)
type JSFunction struct {
	Name       string         `json:"name"`
	Params     []string       `json:"params,omitempty"`
	ReturnType string         `json:"return_type,omitempty"` // TS only
	IsAsync    bool           `json:"is_async,omitempty"`
	IsExported bool           `json:"is_exported,omitempty"`
	IsArrow    bool           `json:"is_arrow,omitempty"`
	IsDefault  bool           `json:"is_default,omitempty"`
	Docstring  string         `json:"docstring,omitempty"`
	FilePath   string         `json:"file_path"`
	StartLine  int            `json:"start_line"`
	EndLine    int            `json:"end_line"`
	Code       string         `json:"code,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// JSClass represents a JS/TS class
type JSClass struct {
	Name       string     `json:"name"`
	Extends    string     `json:"extends,omitempty"`
	Implements []string   `json:"implements,omitempty"` // TS only
	Methods    []JSMethod `json:"methods,omitempty"`
	IsExported bool       `json:"is_exported,omitempty"`
	IsDefault  bool       `json:"is_default,omitempty"`
	IsAbstract bool       `json:"is_abstract,omitempty"` // TS only
	Docstring  string     `json:"docstring,omitempty"`
	FilePath   string     `json:"file_path"`
	StartLine  int        `json:"start_line"`
	EndLine    int        `json:"end_line"`
	Code       string     `json:"code,omitempty"`
}

// JSMethod represents a method inside a JS/TS class
type JSMethod struct {
	Name       string   `json:"name"`
	Params     []string `json:"params,omitempty"`
	ReturnType string   `json:"return_type,omitempty"`
	IsAsync    bool     `json:"is_async,omitempty"`
	IsStatic   bool     `json:"is_static,omitempty"`
	IsPrivate  bool     `json:"is_private,omitempty"`
	Visibility string   `json:"visibility,omitempty"` // public, private, protected (TS)
	Docstring  string   `json:"docstring,omitempty"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
}

// TSInterface represents a TypeScript interface
type TSInterface struct {
	Name       string       `json:"name"`
	Extends    []string     `json:"extends,omitempty"`
	Properties []TSProperty `json:"properties,omitempty"`
	Methods    []TSMethod   `json:"methods,omitempty"`
	IsExported bool         `json:"is_exported,omitempty"`
	Docstring  string       `json:"docstring,omitempty"`
	FilePath   string       `json:"file_path"`
	StartLine  int          `json:"start_line"`
	EndLine    int          `json:"end_line"`
	Code       string       `json:"code,omitempty"`
}

// TSTypeAlias represents a TypeScript type alias
type TSTypeAlias struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	IsExported bool   `json:"is_exported,omitempty"`
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
}

// TSEnum represents a TypeScript enum
type TSEnum struct {
	Name       string   `json:"name"`
	Members    []string `json:"members,omitempty"`
	IsConst    bool     `json:"is_const,omitempty"`
	IsExported bool     `json:"is_exported,omitempty"`
	FilePath   string   `json:"file_path"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
}

// TSProperty represents a property in a TS interface
type TSProperty struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"`
}

// TSMethod represents a method signature in a TS interface
type TSMethod struct {
	Name       string   `json:"name"`
	Params     []string `json:"params,omitempty"`
	ReturnType string   `json:"return_type,omitempty"`
}

// JSImport represents an import statement
type JSImport struct {
	Source    string   `json:"source"`              // Module path/name
	Default   string   `json:"default,omitempty"`   // Default import name
	Named     []string `json:"named,omitempty"`     // Named imports
	Namespace string   `json:"namespace,omitempty"` // import * as X
	IsType    bool     `json:"is_type,omitempty"`   // import type { X } (TS)
	Line      int      `json:"line"`
}

// JSExport represents an export statement
type JSExport struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default,omitempty"`
	Type      string `json:"type,omitempty"` // function, class, const, etc.
	Line      int    `json:"line"`
}
