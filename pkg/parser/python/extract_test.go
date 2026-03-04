package python

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractBaseTypeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple type",
			input:    "int",
			expected: "int",
		},
		{
			name:     "generic type",
			input:    "List[int]",
			expected: "int",
		},
		{
			name:     "nested generic type",
			input:    "Dict[str, List[User]]",
			expected: "User",
		},
		{
			name:     "missing closing bracket",
			input:    "List[",
			expected: "List",
		},
		{
			name:     "missing closing bracket with value",
			input:    "List[int",
			expected: "int",
		},
		{
			name:     "multiple missing closing brackets",
			input:    "Dict[str, List[User",
			expected: "User",
		},
		{
			name:     "empty generic",
			input:    "List[]",
			expected: "List",
		},
		{
			name:     "whitespace",
			input:    "  List[ int ]  ",
			expected: "int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBaseTypeName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
