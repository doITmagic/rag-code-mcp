package skills

import (
	"strings"
	"testing"
)

func TestParseYAMLFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantDesc string
	}{
		{
			name: "standard Anthropic style",
			input: `---
name: DOCX creation
description: Create, edit, and analyze DOCX files
---

# Detailed instructions here...`,
			wantName: "DOCX creation",
			wantDesc: "Create, edit, and analyze DOCX files",
		},
		{
			name: "quoted values",
			input: `---
name: "Go Best Practices"
description: "Go development patterns, project structure, and idiomatic practices"
---`,
			wantName: "Go Best Practices",
			wantDesc: "Go development patterns, project structure, and idiomatic practices",
		},
		{
			name: "single-quoted values",
			input: `---
name: 'PDF Generator'
description: 'Create and manipulate PDF files'
---`,
			wantName: "PDF Generator",
			wantDesc: "Create and manipulate PDF files",
		},
		{
			name: "no frontmatter",
			input: `# Just a regular markdown file

No YAML frontmatter here.`,
			wantName: "",
			wantDesc: "",
		},
		{
			name: "only name, no description",
			input: `---
name: My Skill
---`,
			wantName: "My Skill",
			wantDesc: "",
		},
		{
			name: "empty frontmatter",
			input: `---
---`,
			wantName: "",
			wantDesc: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, desc := parseYAMLFrontmatter(strings.NewReader(tt.input))
			if name != tt.wantName {
				t.Errorf("name: got %q, want %q", name, tt.wantName)
			}
			if desc != tt.wantDesc {
				t.Errorf("description: got %q, want %q", desc, tt.wantDesc)
			}
		})
	}
}
