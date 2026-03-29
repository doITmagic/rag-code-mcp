package gotemplate

// GoTemplate represents a parsed Go template file with all its directives.
type GoTemplate struct {
	FilePath         string
	TotalLines       int
	Defines          []DefineDirective
	Blocks           []BlockDirective
	TemplateIncludes []TemplateInclude
	Ranges           []RangeDirective
	Conditionals     []ConditionalDirective
	WithBlocks       []WithDirective
	Variables        []string
	CustomFuncs      []string
	Comments         []string
}

// DefineDirective represents a {{ define "name" }} ... {{ end }} block.
type DefineDirective struct {
	Name    string
	Line    int
	EndLine int
}

// BlockDirective represents a {{ block "name" pipeline }} ... {{ end }} construct.
type BlockDirective struct {
	Name     string
	Pipeline string // the pipeline after name, e.g. "."
	Line     int
	EndLine  int
}

// TemplateInclude represents a {{ template "name" pipeline }} call.
type TemplateInclude struct {
	Name     string
	Pipeline string // e.g. "." or ".Data"
	Line     int
}

// RangeDirective represents a {{ range pipeline }} ... {{ end }} loop.
type RangeDirective struct {
	Variable string // what is iterated, e.g. ".Items"
	Line     int
	EndLine  int
}

// ConditionalDirective represents a {{ if pipeline }} ... {{ end }} conditional.
type ConditionalDirective struct {
	Condition string
	HasElse   bool
	Line      int
	EndLine   int
}

// WithDirective represents a {{ with pipeline }} ... {{ end }} scoping block.
type WithDirective struct {
	Pipeline string
	Line     int
	EndLine  int
}
