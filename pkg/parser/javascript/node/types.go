package node

// NodeInfo contains Node.js-specific analysis results
type NodeInfo struct {
	Routes        []Route        `json:"routes,omitempty"`
	Middleware    []Middleware   `json:"middleware,omitempty"`
	Requires      []Require      `json:"requires,omitempty"`
	ModuleExports []ModuleExport `json:"module_exports,omitempty"`
}

// Route represents an Express/HTTP route definition
type Route struct {
	Method   string `json:"method"` // get, post, put, delete, patch, all, use
	Path     string `json:"path"`
	Handler  string `json:"handler,omitempty"`   // Handler function name
	IsRouter bool   `json:"is_router,omitempty"` // Router-level route
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// Middleware represents Express middleware usage
type Middleware struct {
	Name     string `json:"name"`
	IsCustom bool   `json:"is_custom"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// Require represents a CommonJS require() call
type Require struct {
	Module  string `json:"module"`
	Binding string `json:"binding"`  // Variable name bound to
	IsLocal bool   `json:"is_local"` // ./local vs external module
	Line    int    `json:"line"`
}

// ModuleExport represents a module.exports assignment
type ModuleExport struct {
	Name     string `json:"name,omitempty"`
	Type     string `json:"type"` // function, class, object, value
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}
