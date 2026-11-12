package web

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/odvcencio/gotreesitter"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

type htmlAnalyzer struct{}

func (htmlAnalyzer) Language() string { return "html" }
func (htmlAnalyzer) Extensions() []string {
	return []string{".html", ".htm", ".gohtml", ".tmpl", ".tpl"}
}

// sectional tags get a symbol even without an id and own the elements
// inside them.
var sectional = map[string]bool{
	"header": true, "nav": true, "main": true, "section": true, "article": true,
	"aside": true, "footer": true, "form": true, "dialog": true, "template": true,
}

type htmlCtx struct {
	f      parser.File
	lang   *gotreesitter.Language
	module string
	lines  lineIndex
	syms   []parser.Symbol // syms[0] is the document
}

func (htmlAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	tree, lang, err := parser.ParseTree("html", f.Source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	c := &htmlCtx{f: f, lang: lang, module: moduleName(f), lines: newLineIndex(f.Source)}
	name := stem(f.Path)
	c.syms = append(c.syms, parser.Symbol{
		Name: name, Kind: parser.KindTemplate, Module: c.module, Exported: true,
		Content: capLines(string(f.Source), maxContentLines), StartLine: 1, EndLine: len(c.lines),
	})
	// A document longer than the cap is also split into fixed-size parts so
	// the whole text stays searchable.
	for i, from := 2, maxContentLines+1; from <= len(c.lines) && len(c.syms) < maxSymbols; i, from = i+1, from+maxContentLines {
		to := min(from+maxContentLines-1, len(c.lines))
		c.syms = append(c.syms, parser.Symbol{
			Name: name + "#" + strconv.Itoa(i), Kind: parser.KindTemplate, Module: c.module, Exported: true,
			Content: c.lines.text(f.Source, from, to), StartLine: from, EndLine: to,
		})
	}
	root := tree.RootNode()
	if first := root.NamedChild(0); first != nil && first.Type(lang) == "comment" {
		c.syms[0].Doc = trimComment(parser.Text(first, f.Source))
	}
	c.walk(root, "")
	c.goTemplates()
	c.handlers()
	return parser.Finalize(f, c.syms), nil
}

// reHandler matches an inline event handler attribute (onclick="..."),
// reCall a plain call in its code: not a member (`.f(`), not a keyword.
var (
	reHandler  = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reCall     = regexp.MustCompile(`(?:^|[^\w$.])([A-Za-z_$][\w$]*)\s*\(`)
	reGoAction = regexp.MustCompile(`\{\{.*?\}\}`)
	// reString is a JavaScript string literal: the words in it are no calls.
	reString = regexp.MustCompile(`'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|` + "`(?:[^`\\\\]|\\\\.)*`")
	keywords   = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "return": true, "function": true,
		"typeof": true, "new": true, "catch": true, "void": true, "delete": true, "await": true, "in": true, "of": true}
)

// handlers adds a call to window.f for each f() in an inline event handler,
// on the narrowest symbol spanning its line: the code runs in the page's
// global scope, where the index finds the script function f by name.
// shortcut: scanned with a regex over the raw source, like the Go template
// actions; `ns.f()` and handlers in inline scripts are not tracked.
func (c *htmlCtx) handlers() {
	for _, m := range reHandler.FindAllSubmatchIndex(c.f.Source, -1) {
		from, to := m[2], m[3]
		if from < 0 {
			from, to = m[4], m[5]
		}
		code := reGoAction.ReplaceAllLiteral(c.f.Source[from:to], []byte("0")) // resolveError({{.ID}})
		code = reString.ReplaceAllLiteral(code, []byte(`""`))
		line := c.lines.lineOf(from)
		var owner *parser.Symbol
		for i := range c.syms {
			if s := &c.syms[i]; s.StartLine <= line && line <= s.EndLine && (owner == nil || s.EndLine-s.StartLine < owner.EndLine-owner.StartLine) {
				owner = s
			}
		}
		for _, cm := range reCall.FindAllSubmatch(code, -1) {
			if name := string(cm[1]); !keywords[name] {
				owner.Relations = append(owner.Relations, parser.Relation{Kind: parser.RelCall, TargetName: name, TargetQualifiedName: "window." + name, Line: line})
			}
		}
	}
}

// walk emits one symbol per element with an id or a sectional tag and
// records script/stylesheet references on the document.
func (c *htmlCtx) walk(n *gotreesitter.Node, owner string) {
	doc := ""
	for _, ch := range parser.NamedChildren(n) {
		if !(len(c.syms) < maxSymbols) {
			break
		}
		switch ch.Type(c.lang) {
		case "comment":
			doc = trimComment(parser.Text(ch, c.f.Source))
			continue
		case "style_element":
		case "script_element": // inline scripts are not parsed
			if _, attrs, _ := c.openTag(ch); attrs["src"] != "" {
				c.addImport(attrs["src"], ch)
			}
		case "element":
			tag, attrs, open := c.openTag(ch)
			if tag == "link" && strings.EqualFold(attrs["rel"], "stylesheet") && attrs["href"] != "" {
				c.addImport(attrs["href"], ch)
			}
			if tag == "title" && c.syms[0].Signature == "" {
				c.syms[0].Signature = collapse(innerText(ch, c.lang, c.f.Source))
			}
			name, id := "", strings.TrimSpace(attrs["id"])
			switch {
			case sectional[tag] && id != "":
				name = tag + "#" + id
			case sectional[tag]:
				name = tag
			case id != "":
				name = id
			}
			childOwner := owner
			if name != "" {
				span := ch
				if voidTags[tag] && open != nil { // no end tag: the element node runs over the whitespace after it
					span = open
				}
				start, end := parser.Lines(span)
				c.syms = append(c.syms, parser.Symbol{
					Name: name, Kind: parser.KindTemplate, Module: c.module, Owner: owner, Exported: true,
					Signature: collapse(parser.Text(open, c.f.Source)), Doc: doc,
					Content: capLines(parser.Text(span, c.f.Source), maxContentLines), StartLine: start, EndLine: end,
				})
				if sectional[tag] {
					childOwner = name
				}
			}
			c.walk(ch, childOwner)
		default: // ERROR and other wrappers: keep looking for elements
			c.walk(ch, owner)
		}
		doc = ""
	}
}

// voidTags are the HTML elements that never have an end tag.
var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

// openTag returns the lower-case tag name and attributes of an element's
// start or self-closing tag.
func (c *htmlCtx) openTag(el *gotreesitter.Node) (string, map[string]string, *gotreesitter.Node) {
	attrs := map[string]string{}
	for _, t := range parser.NamedChildren(el) {
		if typ := t.Type(c.lang); typ != "start_tag" && typ != "self_closing_tag" {
			continue
		}
		tag := ""
		for _, a := range parser.NamedChildren(t) {
			switch a.Type(c.lang) {
			case "tag_name":
				tag = strings.ToLower(parser.Text(a, c.f.Source))
			case "attribute":
				name := strings.ToLower(parser.Text(a.NamedChild(0), c.f.Source))
				val := a.NamedChild(1)
				if val != nil && val.Type(c.lang) == "quoted_attribute_value" {
					val = val.NamedChild(0)
				}
				attrs[name] = parser.Text(val, c.f.Source)
			}
		}
		return tag, attrs, t
	}
	return "", attrs, nil
}

func innerText(el *gotreesitter.Node, lang *gotreesitter.Language, src []byte) string {
	var b strings.Builder
	parser.Walk(el, func(n *gotreesitter.Node) bool {
		if n.Type(lang) == "text" {
			b.WriteString(parser.Text(n, src))
			b.WriteByte(' ')
		}
		return true
	})
	return b.String()
}

func (c *htmlCtx) addImport(ref string, n *gotreesitter.Node) {
	line, _ := parser.Lines(n)
	c.syms[0].Relations = append(c.syms[0].Relations, parser.Relation{
		Kind: parser.RelImports, TargetName: ref, TargetQualifiedName: refModule(c.f, ref), Line: line,
	})
}

// ---- Go template actions ----

// reAction matches the actions that open or close a scope, plus template
// calls, capturing the keyword and the quoted name when present.
var reAction = regexp.MustCompile(`\{\{-?\s*(define|block|template|range|if|with|end)\b(?:\s+"([^"]*)")?[^}]*\}\}`)

type action struct {
	kw, name   string
	start, end int // byte offsets of the whole action
}

// goTemplates emits a KindTemplate per {{define}}/{{block}} and a RelCall per
// {{template "x"}} on the innermost define, or on the document.
func (c *htmlCtx) goTemplates() {
	var acts []action
	defined := map[string]bool{}
	for _, m := range reAction.FindAllSubmatchIndex(c.f.Source, -1) {
		a := action{kw: string(c.f.Source[m[2]:m[3]]), start: m[0], end: m[1]}
		if m[4] >= 0 {
			a.name = string(c.f.Source[m[4]:m[5]])
		}
		if (a.kw == "define" || a.kw == "block") && a.name != "" {
			defined[a.name] = true
		}
		acts = append(acts, a)
	}
	type scope struct {
		action
		sym *parser.Symbol // nil for range/if/with
	}
	var stack []scope
	callOwner := func() *parser.Symbol {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].sym != nil {
				return stack[i].sym
			}
		}
		return &c.syms[0]
	}
	closeScope := func(s scope, end int) {
		if s.sym == nil {
			return
		}
		s.sym.EndLine = c.lines.lineOf(end - 1)
		s.sym.Content = capLines(string(c.f.Source[s.start:end]), maxContentLines)
		if len(c.syms) < maxSymbols {
			c.syms = append(c.syms, *s.sym)
		}
	}
	for _, a := range acts {
		switch a.kw {
		case "template":
			tqn := ""
			if defined[a.name] {
				tqn = parser.Qualify(c.module, "", a.name)
			}
			o := callOwner()
			o.Relations = append(o.Relations, parser.Relation{
				Kind: parser.RelCall, TargetName: a.name, TargetQualifiedName: tqn, Line: c.lines.lineOf(a.start),
			})
		case "end":
			if len(stack) == 0 {
				continue
			}
			closeScope(stack[len(stack)-1], a.end)
			stack = stack[:len(stack)-1]
		default:
			s := scope{action: a}
			if (a.kw == "define" || a.kw == "block") && a.name != "" {
				s.sym = &parser.Symbol{
					Name: a.name, Kind: parser.KindTemplate, Module: c.module, Exported: true,
					Signature: string(c.f.Source[a.start:a.end]), StartLine: c.lines.lineOf(a.start),
				}
			}
			stack = append(stack, s)
		}
	}
	for i := len(stack) - 1; i >= 0; i-- { // unterminated scopes run to EOF
		closeScope(stack[i], len(c.f.Source))
	}
}
