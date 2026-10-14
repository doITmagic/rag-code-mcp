package javascript

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCodeAnalyzer_CanHandle(t *testing.T) {
	ca := NewCodeAnalyzer()
	tests := []struct {
		file      string
		canHandle bool
	}{
		{"app.js", true},
		{"component.jsx", true},
		{"service.ts", true},
		{"page.tsx", true},
		{"module.mjs", true},
		{"common.cjs", true},
		{"App.vue", true},
		{"styles.css", false},
		{"main.go", false},
		{"README.md", false},
	}
	for _, tt := range tests {
		got := ca.CanHandle(tt.file)
		if got != tt.canHandle {
			t.Errorf("CanHandle(%q) = %v, want %v", tt.file, got, tt.canHandle)
		}
	}
}

func TestCodeAnalyzer_Analyze_JS(t *testing.T) {
	ca := NewCodeAnalyzer()
	dir := t.TempDir()
	jsFile := filepath.Join(dir, "service.js")
	content := `
export function greet(name) {
  return "Hello " + name;
}

export class UserService {
  getUser(id) { return id; }
}
`
	if err := os.WriteFile(jsFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ca.Analyze(context.Background(), jsFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Symbols) == 0 {
		t.Error("expected at least 1 symbol")
	}
}

func TestCodeAnalyzer_Analyze_Vue(t *testing.T) {
	ca := NewCodeAnalyzer()
	dir := t.TempDir()
	vueFile := filepath.Join(dir, "Button.vue")
	content := `<template>
  <button @click="handleClick">{{ label }}</button>
</template>

<script setup>
import { ref, computed } from 'vue';
import { useAuth } from './auth';

const label = ref('Click me');
const upper = computed(() => label.value.toUpperCase());
const auth1 = useAuth();
const auth2 = useAuth();

function handleClick() {
  console.log('clicked');
}
</script>

<style scoped>
button { padding: 8px; }
</style>
`
	if err := os.WriteFile(vueFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ca.Analyze(context.Background(), vueFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for .vue file")
	}

	var foundButton bool
	var useAuthCount int
	for _, sym := range result.Symbols {
		if sym.Name == "ref" || sym.Name == "computed" {
			t.Errorf("built-in Vue composable %q should not be emitted as a function symbol", sym.Name)
		}
		if sym.Name == "Button" {
			foundButton = true
			if sym.StartLine != 1 || sym.EndLine <= 1 {
				t.Errorf("Button component symbol lines = %d..%d, want 1..>1", sym.StartLine, sym.EndLine)
			}
		}
		if sym.Name == "useAuth" {
			useAuthCount++
			if sym.StartLine <= 0 || sym.EndLine <= 0 {
				t.Errorf("useAuth symbol lines = %d..%d, want >0", sym.StartLine, sym.EndLine)
			}
		}
	}
	if !foundButton {
		t.Error("expected Button component symbol")
	}
	if useAuthCount != 1 {
		t.Errorf("expected useAuth custom composable once (deduplicated), got %d", useAuthCount)
	}
}

func TestCodeAnalyzer_Analyze_VueWithOptions(t *testing.T) {
	ca := NewCodeAnalyzer()
	dir := t.TempDir()
	vueFile := filepath.Join(dir, "Counter.vue")
	content := `<template>
  <div>{{ count }}</div>
</template>

<script>
export default {
  name: 'Counter',
  data() {
    return { count: 0 };
  },
  methods: {
    increment() { this.count++; }
  }
};
</script>
`
	if err := os.WriteFile(vueFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ca.Analyze(context.Background(), vueFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCodeAnalyzer_Analyze_Directory(t *testing.T) {
	ca := NewCodeAnalyzer()
	dir := t.TempDir()

	// JS file
	jsFile := filepath.Join(dir, "utils.js")
	_ = os.WriteFile(jsFile, []byte("export function add(a, b) { return a + b; }"), 0644)

	// TS file
	tsFile := filepath.Join(dir, "types.ts")
	_ = os.WriteFile(tsFile, []byte("export interface User { id: string; name: string; }"), 0644)

	// Vue file
	vueFile := filepath.Join(dir, "App.vue")
	_ = os.WriteFile(vueFile, []byte(`<script>export default { name: 'App' };</script>`), 0644)

	// Should skip these
	_ = os.WriteFile(filepath.Join(dir, "utils.test.js"), []byte("// test"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "styles.css"), []byte("/* css */"), 0644)

	result, err := ca.Analyze(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Symbols) == 0 {
		t.Error("expected symbols from directory analysis")
	}
}
