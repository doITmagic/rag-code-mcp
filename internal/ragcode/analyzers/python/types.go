package python

import "github.com/doITmagic/rag-code-mcp/internal/codetypes"

// ModuleInfo contains comprehensive information about a Python module/package
type ModuleInfo struct {
	Name        string         `json:"name"`        // Module name (e.g., "mypackage.mymodule")
	Path        string         `json:"path"`        // File path
	Description string         `json:"description"` // Module docstring
	Classes     []ClassInfo    `json:"classes"`
	Functions   []FunctionInfo `json:"functions"`
	Constants   []ConstantInfo `json:"constants"`
	Variables   []VariableInfo `json:"variables"`
	Imports     []ImportInfo   `json:"imports"`
}

// ClassInfo describes a Python class
type ClassInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"` // Class docstring
	Bases       []string       `json:"bases,omitempty"`
	Decorators  []string       `json:"decorators,omitempty"`
	Methods     []MethodInfo   `json:"methods"`
	Properties  []PropertyInfo `json:"properties"`
	ClassVars   []VariableInfo `json:"class_vars,omitempty"`
	IsAbstract  bool           `json:"is_abstract"`
	IsDataclass bool           `json:"is_dataclass"`
	FilePath    string         `json:"file_path,omitempty"`
	StartLine   int            `json:"start_line,omitempty"`
	EndLine     int            `json:"end_line,omitempty"`
	Code        string         `json:"code,omitempty"`
}

// MethodInfo describes a class method
type MethodInfo struct {
	Name        string                 `json:"name"`
	Signature   string                 `json:"signature"`
	Description string                 `json:"description"` // Method docstring
	Parameters  []codetypes.ParamInfo  `json:"parameters"`
	ReturnType  string                 `json:"return_type,omitempty"`
	Returns     []codetypes.ReturnInfo `json:"returns,omitempty"`
	Decorators  []string               `json:"decorators,omitempty"`
	IsStatic    bool                   `json:"is_static"`
	IsClassMethod bool                 `json:"is_classmethod"`
	IsProperty  bool                   `json:"is_property"`
	IsAbstract  bool                   `json:"is_abstract"`
	IsAsync     bool                   `json:"is_async"`
	ClassName   string                 `json:"class_name,omitempty"`
	FilePath    string                 `json:"file_path,omitempty"`
	StartLine   int                    `json:"start_line,omitempty"`
	EndLine     int                    `json:"end_line,omitempty"`
	Code        string                 `json:"code,omitempty"`
}

// FunctionInfo describes a module-level function
type FunctionInfo struct {
	Name        string                 `json:"name"`
	Signature   string                 `json:"signature"`
	Description string                 `json:"description"` // Function docstring
	Parameters  []codetypes.ParamInfo  `json:"parameters"`
	ReturnType  string                 `json:"return_type,omitempty"`
	Returns     []codetypes.ReturnInfo `json:"returns,omitempty"`
	Decorators  []string               `json:"decorators,omitempty"`
	IsAsync     bool                   `json:"is_async"`
	IsGenerator bool                   `json:"is_generator"`
	FilePath    string                 `json:"file_path,omitempty"`
	StartLine   int                    `json:"start_line,omitempty"`
	EndLine     int                    `json:"end_line,omitempty"`
	Code        string                 `json:"code,omitempty"`
}

// PropertyInfo describes a class property (using @property decorator)
type PropertyInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // Type hint if available
	Description string `json:"description"`
	HasGetter   bool   `json:"has_getter"`
	HasSetter   bool   `json:"has_setter"`
	HasDeleter  bool   `json:"has_deleter"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// VariableInfo describes a module-level or class variable
type VariableInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // Type annotation if available
	Value       string `json:"value,omitempty"`
	Description string `json:"description"`
	IsConstant  bool   `json:"is_constant"` // UPPER_CASE naming convention
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// ConstantInfo describes a module-level constant (UPPER_CASE)
type ConstantInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Value       string `json:"value"`
	Description string `json:"description"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// ImportInfo describes an import statement
type ImportInfo struct {
	Module    string   `json:"module"`              // Module being imported
	Names     []string `json:"names,omitempty"`     // Specific names imported (from X import a, b)
	Alias     string   `json:"alias,omitempty"`     // Import alias (import X as Y)
	IsFrom    bool     `json:"is_from"`             // True if "from X import Y"
	StartLine int      `json:"start_line,omitempty"`
}

// DocstringInfo contains parsed docstring information
type DocstringInfo struct {
	Summary     string              `json:"summary"`
	Description string              `json:"description"`
	Args        []DocstringArg      `json:"args,omitempty"`
	Returns     *DocstringReturn    `json:"returns,omitempty"`
	Raises      []DocstringRaise    `json:"raises,omitempty"`
	Examples    []string            `json:"examples,omitempty"`
	Attributes  []DocstringArg      `json:"attributes,omitempty"`
}

// DocstringArg represents a parameter/attribute in docstring
type DocstringArg struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
}

// DocstringReturn represents return value documentation
type DocstringReturn struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description"`
}

// DocstringRaise represents an exception that can be raised
type DocstringRaise struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}
