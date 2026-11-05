package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// Independent verification probes: a fresh HTML/Go-template fixture, a fresh
// SCSS fixture with nested @media/@include, and malformed/binary inputs that
// must neither panic nor hang.

func TestZZVerifyHTML(t *testing.T) {
	m := byQN(analyze(t, "/ws/web/shop/cart.html", `<!DOCTYPE html>
<html lang="en">
<head>
  <LINK REL="Stylesheet" HREF="../../assets/css/shop.css">
  <link rel="icon" href="fav.ico">
  <script type="module" src="./cart.js"></script>
</head>
<body class="x">
  <div class="wrap">
    <div id="toolbar"><button id="buy">Buy</button></div>
  </div>
  <template id="row-tpl"><tr><td id="cell"></td></tr></template>
  <dialog><p id="msg">hi</p></dialog>
  <aside id="side"><nav id="menu"></nav></aside>
  <style>.x{}</style>
  <section>{{template "price" .}}{{template "missing" .}}</section>
</body>
</html>
{{define "price"}}<span id="p">{{.Price}}</span>{{end}}
`))
	doc := m["web/shop/cart.cart"]
	require.NotEmpty(t, doc.ID)
	assert.Equal(t, []rel{{"../../assets/css/shop.css", "assets/css/shop", 4}, {"./cart.js", "web/shop/cart", 6}}, rels(doc, parser.RelImports))
	assert.Equal(t, []rel{{"price", "web/shop/cart.price", 16}, {"missing", "", 16}}, rels(doc, parser.RelCall))
	for qn, owner := range map[string]string{
		"web/shop/cart.toolbar": "", "web/shop/cart.buy": "",
		"web/shop/cart.template#row-tpl": "", "web/shop/cart.template#row-tpl.cell": "template#row-tpl",
		"web/shop/cart.dialog": "", "web/shop/cart.dialog.msg": "dialog",
		"web/shop/cart.aside#side": "", "web/shop/cart.aside#side.nav#menu": "aside#side",
		"web/shop/cart.section": "", "web/shop/cart.price": "", "web/shop/cart.p": "",
	} {
		s, ok := m[qn]
		require.True(t, ok, qn)
		assert.Equal(t, owner, s.Owner, qn)
		assert.Equal(t, parser.KindTemplate, s.Kind, qn)
	}
	assert.Equal(t, 19, m["web/shop/cart.price"].StartLine)
	assert.Equal(t, `{{define "price"}}<span id="p">{{.Price}}</span>{{end}}`, m["web/shop/cart.price"].Content)
	_, hasStyle := m["web/shop/cart.style"]
	assert.False(t, hasStyle)
}

func TestZZRobust(t *testing.T) {
	bin := make([]byte, 4096)
	for i := range bin {
		bin[i] = byte(i*131 + 7)
	}
	inputs := []string{"", "<", "{", "{{", "{{define", "{{define \"a\"}}", "{{end}}{{end}}", "\x00", "\xff\xfe", string(bin),
		"<div id=\"a\"", "<!--", "<!-- x", "<script src=", "<link rel=stylesheet href=>", ".a{", "@media{", "@media (x){.a{",
		"$x", "=", "@", "@mixin", "@include", "@import", "@import ;", "@use", "}}}", "<template",
		"<html><body><p>" + strings.Repeat("<div id='x'>", 500)}
	for _, ext := range []string{".html", ".tmpl"} {
		for i, in := range inputs {
			t.Run(fmt.Sprintf("%s/%d", ext, i), func(t *testing.T) {
				done := make(chan struct{})
				go func() {
					defer close(done)
					_, err := parser.Analyze(parser.File{Path: "/ws/p" + ext, Root: "/ws", Source: []byte(in)})
					assert.NoError(t, err)
				}()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("hang")
				}
			})
		}
	}
}
