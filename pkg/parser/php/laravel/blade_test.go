package laravel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// writeTempBlade creates a temp .blade.php file inside resources/views/ and returns its path.
func writeTempBlade(t *testing.T, dir, name, content string) string {
	t.Helper()
	viewsDir := filepath.Join(dir, "resources", "views")
	fullPath := filepath.Join(viewsDir, name)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return fullPath
}

func TestBladeAnalyzer_Extends(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "pages/home.blade.php",
		`@extends('layouts.app')
<div>Hello</div>`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]
	if tpl.Extends != "layouts.app" {
		t.Errorf("Extends = %q, want %q", tpl.Extends, "layouts.app")
	}
	if tpl.Name != "pages.home" {
		t.Errorf("Name = %q, want %q", tpl.Name, "pages.home")
	}
}

func TestBladeAnalyzer_Sections(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "layouts/app.blade.php",
		`<html>
@yield('title')
<body>
@yield('content')
@section('sidebar')
  default sidebar
@endsection
</body>
</html>`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]
	if len(tpl.Sections) != 3 {
		t.Fatalf("expected 3 sections, got %d: %+v", len(tpl.Sections), tpl.Sections)
	}

	yields := 0
	sections := 0
	for _, s := range tpl.Sections {
		switch s.Type {
		case "yield":
			yields++
		case "section":
			sections++
		}
	}
	if yields != 2 {
		t.Errorf("expected 2 yields, got %d", yields)
	}
	if sections != 1 {
		t.Errorf("expected 1 section, got %d", sections)
	}
}

func TestBladeAnalyzer_Includes(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "pages/show.blade.php",
		`@extends('layouts.app')
@include('partials.header')
@component('components.alert')
  Alert content
@endcomponent
@each('partials.item', $items, 'item')`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]
	if len(tpl.Includes) != 3 {
		t.Fatalf("expected 3 includes, got %d: %+v", len(tpl.Includes), tpl.Includes)
	}

	types := map[string]int{}
	viewNames := map[string]bool{}
	for _, inc := range tpl.Includes {
		types[inc.Type]++
		viewNames[inc.ViewName] = true
	}
	if types["include"] != 1 || types["component"] != 1 || types["each"] != 1 {
		t.Errorf("unexpected include types: %v", types)
	}
	// Verify actual captured view names (not garbage from multi-arg forms)
	for _, expected := range []string{"partials.header", "components.alert", "partials.item"} {
		if !viewNames[expected] {
			t.Errorf("missing expected view name %q, got %v", expected, viewNames)
		}
	}
}

func TestBladeAnalyzer_PushStack(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "layouts/app.blade.php",
		`<head>
@stack('styles')
</head>
<body>
@stack('scripts')
</body>`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	if len(templates[0].Stacks) != 2 {
		t.Errorf("expected 2 stacks, got %d: %v", len(templates[0].Stacks), templates[0].Stacks)
	}
}

func TestBladeAnalyzer_Props(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "components/alert.blade.php",
		`@props(['title', 'color', 'dismissible'])
<div class="{{ $color }}">
  <h2>{{ $title }}</h2>
  {{ $slot }}
</div>`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	if len(templates[0].Props) != 3 {
		t.Errorf("expected 3 props, got %d: %v", len(templates[0].Props), templates[0].Props)
	}
}

func TestBladeAnalyzer_MultilineProps(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "components/modal.blade.php",
		`@props([
    'title',
    'color' => 'blue',
    'dismissible' => false,
])
<div>{{ $title }}</div>`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	want := []string{"title", "color", "dismissible"}
	got := templates[0].Props
	if len(got) != len(want) {
		t.Fatalf("Props = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Props[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBladeAnalyzer_MultipleDirectivesOnSameLine(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "pages/inline.blade.php",
		`@extends('layouts.app') @section('title', 'Home') @include('partials.nav') @yield('hero') <x-alert /> <x-forms.input />`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]
	if tpl.Extends != "layouts.app" {
		t.Errorf("Extends = %q, want 'layouts.app'", tpl.Extends)
	}
	if len(tpl.Sections) != 2 {
		t.Errorf("expected 2 sections (section + yield), got %d: %+v", len(tpl.Sections), tpl.Sections)
	}
	if len(tpl.Includes) != 3 {
		t.Errorf("expected 3 includes (1 include + 2 x-components), got %d: %+v", len(tpl.Includes), tpl.Includes)
	}
}

func TestBladeAnalyzer_IncludeVariantsAndXComponents(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "pages/variants.blade.php",
		`@includeIf('partials.optional')
@includeWhen($isAdmin, 'partials.admin')
@includeUnless($isGuest, 'partials.member')
@includeFirst(['custom.header', 'partials.header'])
<x-alert type="error">
  <x-slot:heading>Error</x-slot:heading>
  <x-forms.input name="email" />
</x-alert>
@livewire('user-profile')`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]

	viewNames := map[string]string{}
	for _, inc := range tpl.Includes {
		viewNames[inc.ViewName] = inc.Type
	}

	expectedIncludes := map[string]string{
		"partials.optional": "include",
		"partials.admin":    "include",
		"partials.member":   "include",
		"custom.header":     "include",
		"partials.header":   "include",
		"alert":             "component",
		"forms.input":       "component",
		"user-profile":      "livewire",
	}
	for name, wantType := range expectedIncludes {
		gotType, ok := viewNames[name]
		if !ok {
			t.Errorf("missing expected include/component %q in %+v", name, tpl.Includes)
			continue
		}
		if gotType != wantType {
			t.Errorf("include %q type = %q, want %q", name, gotType, wantType)
		}
	}
	if _, hasSlot := viewNames["slot"]; hasSlot {
		t.Errorf("<x-slot> should not be recorded as a component")
	}
}

func TestBladeAnalyzer_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "empty.blade.php", "")

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template (even if empty), got %d", len(templates))
	}
	tpl := templates[0]
	if tpl.Extends != "" || len(tpl.Sections) != 0 || len(tpl.Includes) != 0 {
		t.Errorf("empty template should have no directives, got %+v", tpl)
	}
}

func TestBladeAnalyzer_NonexistentFile(t *testing.T) {
	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{"/nonexistent/file.blade.php"})

	if len(templates) != 0 {
		t.Errorf("expected 0 templates for nonexistent file, got %d", len(templates))
	}
}

func TestBladeAnalyzer_ComplexTemplate(t *testing.T) {
	dir := t.TempDir()
	fp := writeTempBlade(t, dir, "pages/dashboard.blade.php",
		`@extends('layouts.admin')

@section('title', 'Dashboard')

@section('content')
  <div class="container">
    @include('partials.stats')
    @include('partials.charts')
    @component('components.card')
      <p>Welcome</p>
    @endcomponent
  </div>
@endsection

@push('scripts')
  <script src="chart.js"></script>
@endpush

@push('styles')
  <link rel="stylesheet" href="dashboard.css">
@endpush`)

	ba := NewBladeAnalyzer()
	templates := ba.Analyze([]string{fp})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]

	if tpl.Extends != "layouts.admin" {
		t.Errorf("Extends = %q, want %q", tpl.Extends, "layouts.admin")
	}
	if len(tpl.Sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(tpl.Sections))
	}
	// Verify section names are correctly captured (not "title', 'Dashboard" from multi-arg)
	for _, s := range tpl.Sections {
		if s.Name != "title" && s.Name != "content" {
			t.Errorf("unexpected section name %q, want 'title' or 'content'", s.Name)
		}
	}
	if len(tpl.Includes) != 3 {
		t.Errorf("expected 3 includes (2 include + 1 component), got %d", len(tpl.Includes))
	}
	if len(tpl.Stacks) != 2 {
		t.Errorf("expected 2 stacks (scripts, styles), got %d", len(tpl.Stacks))
	}
	if tpl.TotalLines != 21 {
		t.Errorf("TotalLines = %d, want 21", tpl.TotalLines)
	}
}

func TestBladeViewName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/project/resources/views/layouts/app.blade.php", "layouts.app"},
		{"/project/resources/views/pages/home.blade.php", "pages.home"},
		{"/project/resources/views/welcome.blade.php", "welcome"},
		{"/random/path/file.blade.php", "file"},
	}
	for _, tt := range tests {
		got := bladeViewName(tt.input)
		if got != tt.want {
			t.Errorf("bladeViewName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// A .blade.php outside any Laravel project must still be analysed: the
// extension decides, not the project.
func TestEnricherAppliesToBladeOutsideLaravel(t *testing.T) {
	dir := t.TempDir() // no artisan, no composer.json
	blade := filepath.Join(dir, "show.blade.php")
	if err := os.WriteFile(blade, []byte("@extends('layouts.app')\n@section('content')x@endsection\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Enricher{adapter: NewAdapter()}
	if !e.IsApplicable(php.NewCodeAnalyzer(), []string{blade}) {
		t.Fatal("enricher should apply to a blade file outside Laravel")
	}
	plain := filepath.Join(dir, "plain.php")
	if err := os.WriteFile(plain, []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if e.IsApplicable(php.NewCodeAnalyzer(), []string{plain}) {
		t.Fatal("plain php outside Laravel should not trigger the enricher")
	}

	// Verify end-to-end php.NewAnalyzer().Analyze produces a blade_template symbol
	res, err := php.NewAnalyzer().Analyze(context.Background(), blade)
	if err != nil {
		t.Fatalf("Analyze standalone blade failed: %v", err)
	}
	if len(res.Symbols) != 1 || string(res.Symbols[0].Type) != "blade_template" {
		t.Fatalf("expected 1 blade_template symbol for standalone .blade.php, got %+v", res.Symbols)
	}
}
