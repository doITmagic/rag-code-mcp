package typescript

// TypeScriptInfo holds TypeScript-specific analysis results
type TypeScriptInfo struct {
	Generics    []Generic    // Generic type parameters
	Decorators  []Decorator  // Class/method decorators
	TypeGuards  []TypeGuard  // Type narrowing functions
	Namespaces  []Namespace  // TS namespaces/modules
	DeclFiles   []DeclFile   // .d.ts declaration files
	MappedTypes []MappedType // Mapped/utility types
	Overloads   []Overload   // Function overloads
}

// Generic represents a generic type parameter usage
type Generic struct {
	Name       string   // Function/class/interface name
	TypeParams []string // Type parameters: T, K extends keyof T, etc.
	FilePath   string
	Line       int
}

// Decorator represents a TypeScript decorator
type Decorator struct {
	Name       string // @Component, @Injectable, etc.
	Target     string // class, method, property, parameter
	TargetName string // Name of decorated element
	Args       string // Arguments to decorator
	FilePath   string
	Line       int
}

// TypeGuard represents a type guard function
type TypeGuard struct {
	Name      string // Function name
	ParamName string // Parameter being checked
	GuardType string // The guarded type
	FilePath  string
	Line      int
}

// Namespace represents a TypeScript namespace/module declaration
type Namespace struct {
	Name       string
	IsModule   bool // 'module' vs 'namespace'
	IsExported bool
	FilePath   string
	StartLine  int
	EndLine    int
}

// DeclFile holds info about a .d.ts declaration file
type DeclFile struct {
	ModuleName   string   // declare module 'xxx'
	Declarations []string // Top-level declared types
	FilePath     string
}

// MappedType represents a mapped/utility type usage
type MappedType struct {
	Name     string // Partial, Required, Pick, Omit, Record, etc.
	BaseType string // The type being mapped
	FilePath string
	Line     int
}

// Overload represents a function overload signature
type Overload struct {
	Name       string   // Function name
	Params     []string // Parameter types for this overload
	ReturnType string
	FilePath   string
	Line       int
}
