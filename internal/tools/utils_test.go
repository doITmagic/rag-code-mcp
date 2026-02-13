package tools

import (
	"testing"
)

func TestInferLanguageFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/home/user/main.go", "go"},
		{"/home/user/app.py", "python"},
		{"/home/user/index.js", "javascript"},
		{"/home/user/index.ts", "javascript"},
		{"/home/user/app.php", "php"},
		{"/home/user/page.html", "html"},
		{"/home/user/README.md", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := inferLanguageFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("inferLanguageFromPath(%q) = %q, want %q", tt.path, result, tt.expected)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	if truncateString("hello world", 5) != "hello" {
		t.Fatal("expected truncation")
	}
	if truncateString("hi", 10) != "hi" {
		t.Fatal("expected no truncation")
	}
}
