package ragcode

import (
	"testing"
)

func TestAnalyzerManager_CodeAnalyzerForProjectType_Go(t *testing.T) {
	mgr := NewAnalyzerManager()

	tests := []struct {
		name        string
		projectType string
		shouldExist bool
	}{
		{"empty string defaults to Go", "", true},
		{"go", "go", true},
		{"Go uppercase", "Go", true},
		{"unknown defaults to Go", "unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := mgr.CodeAnalyzerForProjectType(tt.projectType)
			if tt.shouldExist && analyzer == nil {
				t.Errorf("Expected non-nil analyzer for project type '%s'", tt.projectType)
			}
			if !tt.shouldExist && analyzer != nil {
				t.Errorf("Expected nil analyzer for project type '%s'", tt.projectType)
			}
		})
	}
}

func TestAnalyzerManager_CodeAnalyzerForProjectType_PHP(t *testing.T) {
	mgr := NewAnalyzerManager()

	tests := []struct {
		name        string
		projectType string
		shouldExist bool
	}{
		{"php", "php", true},
		{"PHP uppercase", "PHP", true},
		{"php-laravel", "php-laravel", true},
		{"laravel", "laravel", true},
		{"Laravel uppercase", "Laravel", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := mgr.CodeAnalyzerForProjectType(tt.projectType)
			if tt.shouldExist && analyzer == nil {
				t.Errorf("Expected non-nil analyzer for project type '%s'", tt.projectType)
			}
			if !tt.shouldExist && analyzer != nil {
				t.Errorf("Expected nil analyzer for project type '%s'", tt.projectType)
			}
		})
	}
}

func TestAnalyzerManager_CodeAnalyzerForProjectType_Python(t *testing.T) {
	mgr := NewAnalyzerManager()

	tests := []struct {
		name        string
		projectType string
		shouldExist bool
	}{
		{"python", "python", true},
		{"Python uppercase", "Python", true},
		{"py", "py", true},
		{"django", "django", true},
		{"flask", "flask", true},
		{"fastapi", "fastapi", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := mgr.CodeAnalyzerForProjectType(tt.projectType)
			if tt.shouldExist && analyzer == nil {
				t.Errorf("Expected non-nil analyzer for project type '%s'", tt.projectType)
			}
			if !tt.shouldExist && analyzer != nil {
				t.Errorf("Expected nil analyzer for project type '%s'", tt.projectType)
			}
		})
	}
}

func TestAnalyzerManager_CodeAnalyzerForProjectType_Unknown(t *testing.T) {
	mgr := NewAnalyzerManager()

	tests := []struct {
		name        string
		projectType string
		shouldExist bool
	}{
		{"rust (not implemented)", "rust", false},
		{"javascript (not implemented)", "javascript", false},
		{"java (not implemented)", "java", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := mgr.CodeAnalyzerForProjectType(tt.projectType)
			if tt.shouldExist && analyzer == nil {
				t.Errorf("Expected non-nil analyzer for project type '%s'", tt.projectType)
			}
			if !tt.shouldExist && analyzer != nil {
				t.Errorf("Expected nil analyzer for project type '%s'", tt.projectType)
			}
		})
	}
}

func TestNormalizeProjectType(t *testing.T) {
	tests := []struct {
		input    string
		expected Language
	}{
		{"", LanguageGo},
		{"go", LanguageGo},
		{"Go", LanguageGo},
		{"GO", LanguageGo},
		{"unknown", LanguageGo},
		{"php", LanguagePHP},
		{"PHP", LanguagePHP},
		{"php-laravel", LanguagePHP},
		{"laravel", LanguagePHP},
		{"Laravel", LanguagePHP},
		{"  php  ", LanguagePHP},
		{"python", LanguagePython},
		{"Python", LanguagePython},
		{"py", LanguagePython},
		{"django", LanguagePython},
		{"flask", LanguagePython},
		{"fastapi", LanguagePython},
		{"rust", Language("rust")},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeProjectType(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeProjectType(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
