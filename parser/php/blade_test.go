package php

import (
	"fmt"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const viewSrc = `@extends('layouts.app')

@section('title', 'Users')

@section('content')
    <h1>{{ $user->name }}</h1>
    @include('partials.nav', ['active' => 'users'])
    @includeIf('partials.flash')
    @each('users.row', $users, 'user')
    @livewire('counter')
@endsection

@component('alert')
    <p>Careful</p>
@endcomponent

@section('scripts')
<script></script>
@stop
`

func TestBlade(t *testing.T) {
	path := "/root/resources/views/users/show.blade.php"
	syms, err := parser.Analyze(parser.File{Path: path, Root: "/root", Source: []byte(viewSrc)})
	require.NoError(t, err)

	byQN := map[string]parser.Symbol{}
	for _, s := range syms {
		assert.Equal(t, parser.KindTemplate, s.Kind)
		assert.Equal(t, "php", s.Language)
		assert.True(t, s.Exported)
		byQN[s.QualifiedName] = s
	}

	file := byQN["users.show"]
	assert.Equal(t, "show", file.Name)
	assert.Equal(t, "users", file.Module)
	assert.Equal(t, "", file.Owner)
	assert.Equal(t, 1, file.StartLine)
	assert.Equal(t, 19, file.EndLine)
	assert.Equal(t, viewSrc, file.Content)
	assert.Equal(t, []parser.Relation{
		{Kind: parser.RelImports, TargetName: "layouts.app", TargetQualifiedName: "layouts.app", Line: 1},
		{Kind: parser.RelImports, TargetName: "partials.nav", TargetQualifiedName: "partials.nav", Line: 7},
		{Kind: parser.RelImports, TargetName: "partials.flash", TargetQualifiedName: "partials.flash", Line: 8},
		{Kind: parser.RelImports, TargetName: "users.row", TargetQualifiedName: "users.row", Line: 9},
		{Kind: parser.RelImports, TargetName: "livewire.counter", TargetQualifiedName: "livewire.counter", Line: 10},
		{Kind: parser.RelUsesType, TargetName: "Counter", TargetQualifiedName: `App\Livewire\Counter`, Line: 10},
		{Kind: parser.RelImports, TargetName: "alert", TargetQualifiedName: "alert", Line: 13},
	}, file.Relations)

	tests := []struct {
		qn         string
		start, end int
		signature  string
	}{
		{"users.show.title", 3, 3, "@section('title',"},
		{"users.show.content", 5, 11, "@section('content')"},
		{"users.show.counter", 10, 10, "@livewire('counter')"},
		{"users.show.alert", 13, 15, "@component('alert')"},
		{"users.show.scripts", 17, 19, "@section('scripts')"},
	}
	for _, tt := range tests {
		t.Run(tt.qn, func(t *testing.T) {
			s, ok := byQN[tt.qn]
			require.True(t, ok, "missing %s", tt.qn)
			assert.Equal(t, "show", s.Owner)
			assert.Equal(t, "users", s.Module)
			assert.Equal(t, tt.start, s.StartLine)
			assert.Equal(t, tt.end, s.EndLine)
			assert.Equal(t, tt.signature, s.Signature)
		})
	}
	assert.Contains(t, byQN["users.show.content"].Content, "@endsection")
	assert.Len(t, syms, 6)
}

func TestBladeOutsideViews(t *testing.T) {
	syms, err := parser.Analyze(parser.File{Path: "/root/mail.blade.php", Source: []byte("<p>@section('x')</p>")})
	require.NoError(t, err)
	require.Len(t, syms, 2)
	assert.Equal(t, "mail", syms[0].QualifiedName)
	assert.Equal(t, "mail.x", syms[1].QualifiedName)
	assert.Equal(t, 1, syms[1].EndLine, "unterminated block ends where it starts")
}

// The newline ending a view's last line does not add a line to the file
// symbol.
func TestBladeEndLine(t *testing.T) {
	syms, err := parser.Analyze(parser.File{Path: "/root/resources/views/a.blade.php", Root: "/root", Source: []byte("<p>one</p>\n<p>two</p>\n")})
	require.NoError(t, err)
	require.NotEmpty(t, syms)
	assert.Equal(t, 2, syms[0].EndLine)
}

// <x-...> tag components import their view under components/; directives
// inside {{-- --}} comments are ignored, also over several lines.
func TestBladeTagComponentsAndComments(t *testing.T) {
	src := "{{-- @include('old.nav') --}}\n<x-alert type=\"error\" />\n{{--\n@extends('old.layout')\n@section('dead')\n--}}\n<x-forms.input-text name=\"a\"></x-forms.input-text>\n<x-slot name=\"title\">T</x-slot>\n<x-dynamic-component :component=\"$c\" />\n<x-mail::button />\n@include('partials.nav')\n"
	syms, err := parser.Analyze(parser.File{Path: "/root/resources/views/p.blade.php", Root: "/root", Source: []byte(src)})
	require.NoError(t, err)
	require.Len(t, syms, 1, "no section from inside a comment")
	var got []string
	for _, r := range syms[0].Relations {
		if r.Kind == parser.RelImports {
			got = append(got, fmt.Sprintf("%s:%d", r.TargetQualifiedName, r.Line))
		}
	}
	assert.Equal(t, []string{"components.alert:2", "components.forms.input-text:7", "partials.nav:11"}, got)
	assert.Equal(t, src, syms[0].Content, "the content keeps the comments")
}

// A block nested inside a section does not take the section's end.
func TestBladeNestedBlocks(t *testing.T) {
	src := "a\nb\nc\nd\n@section('content')\n@component('alert')\n@slot('title')\nT\n@endslot\nbody\n@endcomponent\nx\ny\nz\n@endsection\n"
	syms, err := parser.Analyze(parser.File{Path: "/root/resources/views/p.blade.php", Root: "/root", Source: []byte(src)})
	require.NoError(t, err)
	lines := map[string][2]int{}
	for _, s := range syms {
		lines[s.QualifiedName] = [2]int{s.StartLine, s.EndLine}
	}
	assert.Equal(t, [2]int{5, 15}, lines["p.content"])
	assert.Equal(t, [2]int{6, 11}, lines["p.alert"])
}

// A component tag is also a usage of the class component Laravel maps it to
// by convention: <x-forms.input-text> is App\View\Components\Forms\InputText.
func TestBladeClassComponentTag(t *testing.T) {
	src := "<x-alert type=\"error\" />\n<x-forms.input-text name=\"a\" />\n<x-slot name=\"t\" />\n"
	syms, err := parser.Analyze(parser.File{Path: "/root/resources/views/p.blade.php", Root: "/root", Source: []byte(src)})
	require.NoError(t, err)
	var got []string
	for _, r := range syms[0].Relations {
		got = append(got, fmt.Sprintf("%s %s %s:%d", r.Kind, r.TargetName, r.TargetQualifiedName, r.Line))
	}
	assert.Equal(t, []string{
		"imports components.alert components.alert:1",
		`uses_type Alert App\View\Components\Alert:1`,
		"imports components.forms.input-text components.forms.input-text:2",
		`uses_type InputText App\View\Components\Forms\InputText:2`,
	}, got)
}
