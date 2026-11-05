package web

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const htmlSample = `<!-- Page layout -->
<!DOCTYPE html>
<html>
<head>
<title>My  Page</title>
<script src="../static/app.js?v=2"></script>
<script src="https://cdn.example.com/lib.js"></script>
<link rel="stylesheet" href="css/site.css">
<script>var inline = 1;</script>
</head>
<body>
<header id="top">
  <nav><a id="home">Home</a></nav>
</header>
<!-- Main content -->
<main>
  <section id="intro"><div id="hero">Hi</div></section>
  <article>text</article>
</main>
<form action="/x"><input id="q"></form>
<footer>f</footer>
</body>
</html>
`

const goTemplateSample = `{{define "base"}}
<html>{{template "nav" .}}
{{block "content" .}}<p>default</p>{{end}}
</html>
{{end}}
{{define "nav"}}<div>{{range .Items}}{{.}}{{end}}</div>{{end}}
{{template "footer" .}}
`

const cssSample = `@import url("reset.css");
@import "vendor/grid.css" screen;
:root {
  --brand: #123;
  --gap: 4px;
}
/* The button */
.btn,
.btn:hover   > span { color: var(--brand); }
@media (max-width: 600px) {
  /* compact */
  .btn { padding: 0 }
  @supports (display: grid) { .grid { display: grid } }
}
@font-face { font-family: X; }
@keyframes spin { from { x: 1 } to { x: 2 } }
`

const scssSample = `@use "sass:math";
@use "base" as b;
@forward "mixins";
@import "vars";
$primary: #333;
$_private: 1px;
// Theme mixin
@mixin theme($t: dark) { color: $t; }
@function double($n) { @return $n * 2; }
.card {
  @include theme(light);
  width: double(2px);
  $inner: 1;
  &:hover { color: red; }
}
@include theme;
`

const sassSample = "@import base\n$c: red\n=round($r)\n  border-radius: $r\n.a\n  color: $c\n\n  .b\n    x: y\n.c\n  z: 1\n"

func analyze(t *testing.T, path, src string) []parser.Symbol {
	t.Helper()
	syms, err := parser.Analyze(parser.File{Path: path, Root: "/ws", Source: []byte(src)})
	require.NoError(t, err)
	for _, s := range syms {
		assert.NotEmpty(t, s.ID)
		assert.Equal(t, parser.Qualify(s.Module, s.Owner, s.Name), s.QualifiedName)
		assert.LessOrEqual(t, s.StartLine, s.EndLine, s.QualifiedName)
	}
	return syms
}

func byQN(syms []parser.Symbol) map[string]parser.Symbol {
	m := map[string]parser.Symbol{}
	for _, s := range syms {
		m[s.QualifiedName] = s
	}
	return m
}

type rel struct {
	target, tqn string
	line        int
}

func rels(s parser.Symbol, kind parser.RelationKind) []rel {
	var out []rel
	for _, r := range s.Relations {
		if r.Kind == kind {
			out = append(out, rel{r.TargetName, r.TargetQualifiedName, r.Line})
		}
	}
	return out
}

func TestHTML(t *testing.T) {
	syms := analyze(t, "/ws/templates/page.html", htmlSample)
	m := byQN(syms)
	require.Len(t, syms, 11)
	cases := []struct {
		qn, owner, sig string
		start, end     int
	}{
		{"templates/page.page", "", "My Page", 1, 23},
		{"templates/page.header#top", "", `<header id="top">`, 12, 14},
		{"templates/page.header#top.nav", "header#top", "<nav>", 13, 13},
		{"templates/page.nav.home", "nav", `<a id="home">`, 13, 13},
		{"templates/page.main", "", "<main>", 16, 19},
		{"templates/page.main.section#intro", "main", `<section id="intro">`, 17, 17},
		{"templates/page.section#intro.hero", "section#intro", `<div id="hero">`, 17, 17},
		{"templates/page.main.article", "main", "<article>", 18, 18},
		{"templates/page.form", "", `<form action="/x">`, 20, 20},
		{"templates/page.form.q", "form", `<input id="q">`, 20, 20},
		{"templates/page.footer", "", "<footer>", 21, 21},
	}
	for _, tc := range cases {
		s, ok := m[tc.qn]
		require.True(t, ok, tc.qn)
		assert.Equal(t, parser.KindTemplate, s.Kind, tc.qn)
		assert.Equal(t, "html", s.Language)
		assert.True(t, s.Exported)
		assert.Equal(t, tc.owner, s.Owner, tc.qn)
		assert.Equal(t, tc.sig, s.Signature, tc.qn)
		assert.Equal(t, tc.start, s.StartLine, tc.qn)
		assert.Equal(t, tc.end, s.EndLine, tc.qn)
	}
	doc := m["templates/page.page"]
	assert.Equal(t, "Page layout", doc.Doc)
	assert.Equal(t, htmlSample, doc.Content)
	assert.Equal(t, "Main content", m["templates/page.main"].Doc)
	assert.Equal(t, "<main>\n  <section id=\"intro\"><div id=\"hero\">Hi</div></section>\n  <article>text</article>\n</main>", m["templates/page.main"].Content)
	assert.Equal(t, []rel{
		{"../static/app.js?v=2", "static/app", 6},
		{"https://cdn.example.com/lib.js", "", 7},
		{"css/site.css", "templates/css/site", 8},
	}, rels(doc, parser.RelImports))
}

func TestGoTemplate(t *testing.T) {
	m := byQN(analyze(t, "/ws/views/layout.gohtml", goTemplateSample))
	doc := m["views/layout.layout"]
	assert.Equal(t, []rel{{"footer", "", 7}}, rels(doc, parser.RelCall))

	base := m["views/layout.base"]
	assert.Equal(t, parser.KindTemplate, base.Kind)
	assert.Equal(t, `{{define "base"}}`, base.Signature)
	assert.Equal(t, 1, base.StartLine)
	assert.Equal(t, 5, base.EndLine)
	assert.True(t, strings.HasPrefix(base.Content, `{{define "base"}}`))
	assert.True(t, strings.HasSuffix(base.Content, "</html>\n{{end}}"))
	assert.Equal(t, []rel{{"nav", "views/layout.nav", 2}}, rels(base, parser.RelCall))

	content := m["views/layout.content"]
	assert.Equal(t, `{{block "content" .}}`, content.Signature)
	assert.Equal(t, 3, content.StartLine)
	assert.Equal(t, 3, content.EndLine)
	assert.Equal(t, `{{block "content" .}}<p>default</p>{{end}}`, content.Content)

	nav := m["views/layout.nav"]
	assert.Equal(t, 6, nav.StartLine)
	assert.Equal(t, 6, nav.EndLine)
	assert.Empty(t, nav.Relations)

	// An unterminated define runs to the end of the file.
	m = byQN(analyze(t, "/ws/v/open.tmpl", "x\n{{define \"a\"}}\n{{if .X}}y\n"))
	a := m["v/open.a"]
	assert.Equal(t, 2, a.StartLine)
	assert.Equal(t, 3, a.EndLine)
}

func TestLongDocumentSplit(t *testing.T) {
	src := strings.Repeat("<p>line</p>\n", 1000)
	syms := analyze(t, "/ws/big.html", src)
	m := byQN(syms)
	require.Len(t, syms, 3)
	assert.Equal(t, 400, strings.Count(m["big.big"].Content, "\n")+1)
	assert.Equal(t, 1000, m["big.big"].EndLine)
	p2, p3 := m["big.big#2"], m["big.big#3"]
	assert.Equal(t, 401, p2.StartLine)
	assert.Equal(t, 800, p2.EndLine)
	assert.Equal(t, 400, strings.Count(p2.Content, "\n")+1)
	assert.Equal(t, 801, p3.StartLine)
	assert.Equal(t, 1000, p3.EndLine)
	assert.Equal(t, 200, strings.Count(p3.Content, "\n")+1)
}

func TestCSS(t *testing.T) {
	syms := analyze(t, "/ws/assets/site.css", cssSample)
	m := byQN(syms)
	require.Len(t, syms, 9)
	cases := []struct {
		qn       string
		kind     parser.Kind
		owner    string
		start    int
		end      int
		sig, doc string
	}{
		{"assets/site.site", parser.KindStyle, "", 1, 2, "stylesheet", ""},
		{"assets/site.:root", parser.KindStyle, "", 3, 6, ":root", ""},
		{"assets/site.--brand", parser.KindVariable, "", 4, 4, "--brand: #123", ""},
		{"assets/site.--gap", parser.KindVariable, "", 5, 5, "--gap: 4px", ""},
		{"assets/site..btn, .btn:hover > span", parser.KindStyle, "", 8, 9, ".btn, .btn:hover > span", "The button"},
		{"assets/site.@media (max-width: 600px)..btn", parser.KindStyle, "@media (max-width: 600px)", 12, 12, ".btn", "compact"},
		{"assets/site.@supports (display: grid)..grid", parser.KindStyle, "@supports (display: grid)", 13, 13, ".grid", ""},
		{"assets/site.@font-face", parser.KindStyle, "", 15, 15, "@font-face", ""},
		{"assets/site.@keyframes spin", parser.KindStyle, "", 16, 16, "@keyframes spin", ""},
	}
	for _, tc := range cases {
		s, ok := m[tc.qn]
		require.True(t, ok, tc.qn)
		assert.Equal(t, tc.kind, s.Kind, tc.qn)
		assert.Equal(t, "css", s.Language)
		assert.True(t, s.Exported, tc.qn)
		assert.Equal(t, tc.owner, s.Owner, tc.qn)
		assert.Equal(t, tc.start, s.StartLine, tc.qn)
		assert.Equal(t, tc.end, s.EndLine, tc.qn)
		assert.Equal(t, tc.sig, s.Signature, tc.qn)
		assert.Equal(t, tc.doc, s.Doc, tc.qn)
	}
	assert.Equal(t, ".btn,\n.btn:hover   > span { color: var(--brand); }", m["assets/site..btn, .btn:hover > span"].Content)
	file := m["assets/site.site"]
	assert.Equal(t, "@import url(\"reset.css\");\n@import \"vendor/grid.css\" screen;", file.Content)
	assert.Equal(t, []rel{{"reset.css", "assets/reset", 1}, {"vendor/grid.css", "assets/vendor/grid", 2}}, rels(file, parser.RelImports))

	long := "." + strings.Repeat("a", 200) + " { x: y }"
	m = byQN(analyze(t, "/ws/l.css", long))
	require.Len(t, m, 1)
	for _, s := range m {
		assert.Len(t, s.Name, 120)
		assert.Equal(t, long, s.Content)
	}
}

func TestSCSS(t *testing.T) {
	syms := analyze(t, "/ws/styles/main.scss", scssSample)
	m := byQN(syms)
	require.Len(t, syms, 7)
	file := m["styles/main.main"]
	assert.Equal(t, []rel{
		{"sass:math", "", 1}, {"base", "styles/base", 2}, {"mixins", "styles/mixins", 3}, {"vars", "styles/vars", 4},
	}, rels(file, parser.RelImports))
	assert.Equal(t, []rel{{"theme", "styles/main.theme", 16}}, rels(file, parser.RelCall))
	assert.Equal(t, 1, file.StartLine)
	assert.Equal(t, 16, file.EndLine)

	assert.Equal(t, parser.KindVariable, m["styles/main.$primary"].Kind)
	assert.True(t, m["styles/main.$primary"].Exported)
	assert.False(t, m["styles/main.$_private"].Exported)
	_, inner := m["styles/main..card.$inner"]
	assert.False(t, inner, "nested variables are not symbols")

	theme := m["styles/main.theme"]
	assert.Equal(t, parser.KindFunction, theme.Kind)
	assert.Equal(t, "@mixin theme($t: dark)", theme.Signature)
	assert.Equal(t, "Theme mixin", theme.Doc)
	assert.Equal(t, "@mixin theme($t: dark) { color: $t; }", theme.Content)
	assert.Equal(t, 8, theme.StartLine)
	assert.Equal(t, "@function double($n)", m["styles/main.double"].Signature)
	assert.Equal(t, parser.KindFunction, m["styles/main.double"].Kind)

	card := m["styles/main..card"]
	assert.Equal(t, parser.KindStyle, card.Kind)
	assert.Equal(t, 10, card.StartLine)
	assert.Equal(t, 15, card.EndLine)
	assert.Equal(t, []rel{{"theme", "styles/main.theme", 11}}, rels(card, parser.RelCall))
	hover := m["styles/main..card.&:hover"]
	assert.Equal(t, ".card", hover.Owner)
	assert.Equal(t, 14, hover.StartLine)

	m = byQN(analyze(t, "/ws/u.scss", ".x { @include unknown; }"))
	assert.Equal(t, []rel{{"unknown", "", 1}}, rels(m["u..x"], parser.RelCall))
}

func TestSass(t *testing.T) {
	syms := analyze(t, "/ws/s/x.sass", sassSample)
	m := byQN(syms)
	require.Len(t, syms, 5)
	assert.Equal(t, []rel{{"base", "s/base", 1}}, rels(m["s/x.x"], parser.RelImports))
	assert.Equal(t, parser.KindVariable, m["s/x.$c"].Kind)
	round := m["s/x.round"]
	assert.Equal(t, parser.KindFunction, round.Kind)
	assert.Equal(t, 3, round.StartLine)
	assert.Equal(t, 4, round.EndLine)
	a := m["s/x..a"]
	assert.Equal(t, parser.KindStyle, a.Kind)
	assert.Equal(t, 5, a.StartLine)
	assert.Equal(t, 9, a.EndLine)
	assert.Equal(t, ".a\n  color: $c\n\n  .b\n    x: y", a.Content)
	assert.Equal(t, 10, m["s/x..c"].StartLine)
	assert.Equal(t, 11, m["s/x..c"].EndLine)
}

func TestModuleAndRefs(t *testing.T) {
	cases := []struct {
		path, root, module string
	}{
		{"/ws/a/b.html", "/ws", "a/b"},
		{"/ws/top.scss", "/ws", "top"},
		{"/elsewhere/x.css", "/ws", "x"},
		{"/ws/x.css", "", "x"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.module, moduleName(parser.File{Path: tc.path, Root: tc.root}), tc.path)
	}
	f := parser.File{Path: "/ws/pub/css/app.css", Root: "/ws"}
	refs := map[string]string{
		"base.css":          "pub/css/base",
		"./x/y.scss?v=1#f":  "pub/css/x/y",
		"../../lib/z.css":   "lib/z",
		"../../../out.css":  "",
		"/abs/site.css":     "",
		"https://c.dn/a.js": "",
		"sass:math":         "",
		"":                  "",
	}
	for ref, want := range refs {
		assert.Equal(t, want, refModule(f, ref), ref)
	}
}

func TestMalformed(t *testing.T) {
	m := byQN(analyze(t, "/ws/bad.html", `<header id="top">ok</header><div id="a"><p>unclosed <section id="s"`))
	require.Contains(t, m, "bad.bad")
	require.Contains(t, m, "bad.header#top")
	m = byQN(analyze(t, "/ws/bad.css", ".a { color: ; } } .b { x"))
	require.Contains(t, m, "bad..a")
	m = byQN(analyze(t, "/ws/bad.scss", "@mixin ( { .x { @include ; } "))
	assert.NotNil(t, m)
	assert.Empty(t, analyze(t, "/ws/empty.css", ""))
	require.Len(t, analyze(t, "/ws/empty.html", ""), 1)
	assert.Len(t, analyze(t, "/ws/e.sass", "\n\n"), 0)
}

func TestSymbolCap(t *testing.T) {
	src := strings.Repeat(".r { a: b }\n", maxSymbols+50)
	assert.Len(t, analyze(t, "/ws/many.css", src), maxSymbols)
	src = strings.Repeat("<div id=\"d\"></div>\n", maxSymbols+50)
	assert.Len(t, analyze(t, "/ws/many.html", src), maxSymbols)
}

func TestRegistered(t *testing.T) {
	for _, tc := range []struct{ path, lang string }{
		{"/ws/a.html", "html"}, {"/ws/a.HTM", "html"}, {"/ws/a.gohtml", "html"}, {"/ws/a.tmpl", "html"}, {"/ws/a.tpl", "html"},
		{"/ws/a.css", "css"}, {"/ws/a.SCSS", "css"}, {"/ws/a.sass", "css"}, {"/ws/a.less", "css"},
	} {
		a := parser.ForFile(tc.path)
		require.NotNil(t, a, tc.path)
		assert.Equal(t, tc.lang, a.Language(), tc.path)
	}
	syms := analyze(t, "/ws/a.less", "@c: red;\n.m() { x: y }\n.a { .m(); color: @c; .b { z: 1 } }\n")
	require.NotEmpty(t, syms)
	assert.Equal(t, "css", syms[0].Language)
}

func TestConcurrent(t *testing.T) {
	wantHTML := analyze(t, "/ws/templates/page.html", htmlSample)
	wantTpl := analyze(t, "/ws/views/layout.gohtml", goTemplateSample)
	wantCSS := analyze(t, "/ws/assets/site.css", cssSample)
	wantSCSS := analyze(t, "/ws/styles/main.scss", scssSample)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				assert.Equal(t, wantHTML, analyze(t, "/ws/templates/page.html", htmlSample))
				assert.Equal(t, wantTpl, analyze(t, "/ws/views/layout.gohtml", goTemplateSample))
				assert.Equal(t, wantCSS, analyze(t, "/ws/assets/site.css", cssSample))
				assert.Equal(t, wantSCSS, analyze(t, "/ws/styles/main.scss", scssSample))
			}
		}()
	}
	wg.Wait()
}
