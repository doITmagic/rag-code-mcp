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
	require.Len(t, analyze(t, "/ws/empty.html", ""), 1)
}

func TestSymbolCap(t *testing.T) {
	src := strings.Repeat("<div id=\"d\"></div>\n", maxSymbols+50)
	assert.Len(t, analyze(t, "/ws/many.html", src), maxSymbols)
}

func TestRegistered(t *testing.T) {
	for _, tc := range []struct{ path, lang string }{
		{"/ws/a.html", "html"}, {"/ws/a.HTM", "html"}, {"/ws/a.gohtml", "html"}, {"/ws/a.tmpl", "html"}, {"/ws/a.tpl", "html"},
	} {
		a := parser.ForFile(tc.path)
		require.NotNil(t, a, tc.path)
		assert.Equal(t, tc.lang, a.Language(), tc.path)
	}
	for _, p := range []string{"/ws/a.css", "/ws/a.scss", "/ws/a.sass", "/ws/a.less"} {
		assert.Nil(t, parser.ForFile(p), p) // stylesheets are not indexed
	}
}

func TestConcurrent(t *testing.T) {
	wantHTML := analyze(t, "/ws/templates/page.html", htmlSample)
	wantTpl := analyze(t, "/ws/views/layout.gohtml", goTemplateSample)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				assert.Equal(t, wantHTML, analyze(t, "/ws/templates/page.html", htmlSample))
				assert.Equal(t, wantTpl, analyze(t, "/ws/views/layout.gohtml", goTemplateSample))
			}
		}()
	}
	wg.Wait()
}

// A void element (<input>, no end tag) spans its own tag only, not the
// whitespace up to the next element.
func TestVoidElementLines(t *testing.T) {
	syms := analyze(t, "/ws/f.html", "<form>\n  <input id=\"a\" name=\"a\">\n  <input id=\"b\">\n  <p id=\"c\">x</p>\n</form>\n")
	got := map[string][2]int{}
	content := map[string]string{}
	for _, s := range syms {
		got[s.Name] = [2]int{s.StartLine, s.EndLine}
		content[s.Name] = s.Content
	}
	assert.Equal(t, [2]int{2, 2}, got["a"])
	assert.Equal(t, [2]int{3, 3}, got["b"])
	assert.Equal(t, [2]int{4, 4}, got["c"])
	assert.Equal(t, `<input id="a" name="a">`, content["a"])
}
