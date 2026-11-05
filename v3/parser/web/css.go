package web

import (
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

type cssAnalyzer struct{}

func (cssAnalyzer) Language() string     { return "css" }
func (cssAnalyzer) Extensions() []string { return []string{".css", ".scss", ".sass", ".less"} }

type cssCtx struct {
	f      parser.File
	lang   *gotreesitter.Language
	root   *gotreesitter.Node
	module string
	mixins map[string]bool // @mixin names defined in this file
	syms   []parser.Symbol
	file   int // index of the file-level symbol that carries imports, -1 until needed
}

func (cssAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	c := &cssCtx{f: f, module: moduleName(f), mixins: map[string]bool{}, file: -1}
	ext := strings.ToLower(filepath.Ext(f.Path))
	if ext == ".sass" {
		c.scanSass()
		return parser.Finalize(f, c.syms), nil
	}
	grammar := "css" // Less is close enough to CSS for a best-effort tree
	if ext == ".scss" {
		grammar = "scss"
	}
	tree, lang, err := parser.ParseTree(grammar, f.Source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	c.lang, c.root = lang, tree.RootNode()
	parser.Walk(c.root, func(n *gotreesitter.Node) bool {
		if n.Type(lang) == "mixin_statement" {
			c.mixins[c.ident(n)] = true
		}
		return true
	})
	c.walk(c.root, "", -1)
	return parser.Finalize(f, c.syms), nil
}

// add appends a symbol and returns its index, or -1 past the cap.
func (c *cssCtx) add(s parser.Symbol) int {
	if len(c.syms) >= maxSymbols {
		return -1
	}
	s.Module = c.module
	c.syms = append(c.syms, s)
	return len(c.syms) - 1
}

// fileSym returns the index of the file-level symbol, creating it on first
// use so files without imports or top-level includes do not get one.
func (c *cssCtx) fileSym(line int) int {
	if c.file < 0 {
		c.file = c.add(parser.Symbol{
			Name: stem(c.f.Path), Kind: parser.KindStyle, Exported: true, Signature: "stylesheet",
			StartLine: line, EndLine: line,
		})
	}
	return c.file
}

// relate attaches a relation to symbol idx, or to the file symbol when idx
// is -1, extending the file symbol's content with the statement text.
func (c *cssCtx) relate(idx int, r parser.Relation, stmt string) {
	if idx < 0 {
		if idx = c.fileSym(r.Line); idx < 0 {
			return
		}
		s := &c.syms[idx]
		s.EndLine = r.Line
		s.Content = strings.TrimSpace(s.Content + "\n" + stmt)
	}
	c.syms[idx].Relations = append(c.syms[idx].Relations, r)
}

func (c *cssCtx) text(n *gotreesitter.Node) string { return parser.Text(n, c.f.Source) }

func (c *cssCtx) child(n *gotreesitter.Node, typ string) *gotreesitter.Node {
	for _, nc := range parser.NamedChildren(n) {
		if ch := nc; ch.Type(c.lang) == typ {
			return ch
		}
	}
	return nil
}

func (c *cssCtx) ident(n *gotreesitter.Node) string { return c.text(c.child(n, "identifier")) }

// header is the statement text before its block, e.g. "@media (max-width: 600px)".
func (c *cssCtx) header(n, block *gotreesitter.Node) string {
	if block == nil {
		return collapse(c.text(n))
	}
	return collapse(string(c.f.Source[n.StartByte():block.StartByte()]))
}

// importTarget returns the path of an @import/@use/@forward statement.
func (c *cssCtx) importTarget(n *gotreesitter.Node) string {
	var target string
	parser.Walk(n, func(m *gotreesitter.Node) bool {
		if target == "" && m.Type(c.lang) == "string_value" {
			target = strings.Trim(c.text(m), `"'`)
		}
		return target == ""
	})
	return target
}

// walk emits symbols for the children of n. owner is the enclosing rule or
// at-rule name; holder is the symbol that collects @include calls.
func (c *cssCtx) walk(n *gotreesitter.Node, owner string, holder int) {
	if n == nil {
		return
	}
	doc := ""
	for _, ch := range parser.NamedChildren(n) {
		if !(len(c.syms) < maxSymbols) {
			break
		}
		start, end := parser.Lines(ch)
		block := c.child(ch, "block")
		switch ch.Type(c.lang) {
		case "comment", "js_comment":
			doc = trimComment(c.text(ch))
			continue
		case "rule_set":
			sel := collapse(c.text(c.child(ch, "selectors")))
			name := capRunes(sel, maxSelector)
			idx := c.add(parser.Symbol{
				Name: name, Kind: parser.KindStyle, Owner: owner, Signature: sel, Doc: doc,
				Content: c.text(ch), StartLine: start, EndLine: end, Exported: !strings.HasPrefix(name, "%"),
			})
			c.walk(block, name, idx)
		case "media_statement", "supports_statement":
			c.walk(block, c.header(ch, block), holder)
		case "at_rule", "keyframes_statement":
			if block == nil {
				block = c.child(ch, "keyframe_block_list")
			}
			if block != nil {
				c.add(parser.Symbol{
					Name: c.header(ch, block), Kind: parser.KindStyle, Owner: owner, Signature: c.header(ch, block),
					Doc: doc, Content: c.text(ch), StartLine: start, EndLine: end, Exported: true,
				})
			}
		case "mixin_statement", "function_statement":
			name := c.ident(ch)
			idx := c.add(parser.Symbol{
				Name: name, Kind: parser.KindFunction, Owner: owner, Signature: c.header(ch, block), Doc: doc,
				Content: c.text(ch), StartLine: start, EndLine: end, Exported: exportedName(name),
			})
			c.walk(block, name, idx)
		case "include_statement":
			name := c.ident(ch)
			tqn := ""
			if c.mixins[name] {
				tqn = parser.Qualify(c.module, "", name)
			}
			c.relate(holder, parser.Relation{Kind: parser.RelCall, TargetName: name, TargetQualifiedName: tqn, Line: start}, c.text(ch))
		case "import_statement", "use_statement", "forward_statement":
			if target := c.importTarget(ch); target != "" {
				c.relate(-1, parser.Relation{Kind: parser.RelImports, TargetName: target, TargetQualifiedName: refModule(c.f, target), Line: start}, c.text(ch))
			}
		case "declaration":
			prop := c.text(c.child(ch, "property_name"))
			if (n == c.root && strings.HasPrefix(prop, "$")) || (owner == ":root" && strings.HasPrefix(prop, "--")) {
				c.add(parser.Symbol{
					Name: prop, Kind: parser.KindVariable, Signature: strings.TrimSuffix(collapse(c.text(ch)), ";"), Doc: doc,
					Content: c.text(ch), StartLine: start, EndLine: end, Exported: exportedName(prop),
				})
			}
		}
		doc = ""
	}
}

// ---- indented Sass ----

// scanSass handles the indented syntax, which no grammar here parses: a
// non-indented line starts a rule, variable, mixin or import, and the
// indented lines below it are the block.
// ponytail: no nesting, includes or owners; use the scss grammar path if
// indented Sass ever matters.
func (c *cssCtx) scanSass() {
	lines := strings.Split(string(c.f.Source), "\n")
	for i := 0; i < len(lines) && len(c.syms) < maxSymbols; i++ {
		l := strings.TrimRight(lines[i], " \t\r")
		if l == "" || l[0] == ' ' || l[0] == '\t' || strings.HasPrefix(l, "//") || strings.HasPrefix(l, "/*") {
			continue
		}
		end := i
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimRight(lines[j], " \t\r")
			if t == "" {
				continue
			}
			if t[0] != ' ' && t[0] != '\t' {
				break
			}
			end = j
		}
		content := strings.Join(lines[i:end+1], "\n")
		line := i + 1
		switch kw, rest, _ := strings.Cut(l, " "); {
		case strings.HasPrefix(l, "$"):
			name, _, _ := strings.Cut(l, ":")
			c.add(parser.Symbol{Name: name, Kind: parser.KindVariable, Signature: l, Content: content, StartLine: line, EndLine: end + 1, Exported: exportedName(name)})
		case kw == "@import" || kw == "@use" || kw == "@forward":
			target := strings.Trim(strings.TrimSuffix(strings.TrimSpace(rest), ";"), `"'`)
			c.relate(-1, parser.Relation{Kind: parser.RelImports, TargetName: target, TargetQualifiedName: refModule(c.f, target), Line: line}, l)
		case kw == "@mixin" || kw == "@function" || strings.HasPrefix(l, "="):
			name := strings.TrimPrefix(rest, "=")
			if kw[0] == '=' {
				name = l[1:]
			}
			name, _, _ = strings.Cut(name, "(")
			name = strings.TrimSpace(name)
			c.add(parser.Symbol{Name: name, Kind: parser.KindFunction, Signature: l, Content: content, StartLine: line, EndLine: end + 1, Exported: exportedName(name)})
		case strings.HasPrefix(l, "@"):
		default:
			name := capRunes(collapse(l), maxSelector)
			c.add(parser.Symbol{Name: name, Kind: parser.KindStyle, Signature: collapse(l), Content: content, StartLine: line, EndLine: end + 1, Exported: !strings.HasPrefix(name, "%")})
		}
		i = end
	}
}
