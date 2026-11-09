package gdscript

import (
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// maxNodes caps the nodes one scene emits; the last one carries
// Metadata truncated=true when the scene had more.
const maxNodes = 500

// shortcut: node Content is capped. Embedded resources (meshes, tile data)
// serialize as megabyte-long lines and one such point bloats every search
// response that carries it. Raise only with a store-side size check.
const maxContent = 8 << 10

// section is one "[kind attr=value ...]" block of a .tscn file and the
// property lines under it.
type section struct {
	kind       string
	attrs      map[string]string
	start, end int // 1-based, end is the last non-blank line
	script     string
	scriptLine int
	body       string
}

func scene(f parser.File) []parser.Symbol {
	mod := module(f)
	secs := sections(f.Source)
	ext := map[string]map[string]string{} // id -> ext_resource attrs
	for _, s := range secs {
		if s.kind == "ext_resource" {
			ext[s.attrs["id"]] = s.attrs
		}
	}
	extPath := func(ref string) string {
		if r, ok := ext[extResourceID(ref)]; ok {
			return r["path"]
		}
		return ""
	}

	var syms []parser.Symbol
	byPath := map[string]int{}       // Godot node path ("." is the root) -> index in syms
	scriptMod := map[string]string{} // node path -> script module
	rootName, truncated := "", false
	for _, s := range secs {
		if s.kind != "node" {
			continue
		}
		if len(syms) == maxNodes {
			truncated = true
			break
		}
		name, parent := s.attrs["name"], s.attrs["parent"]
		if name == "" {
			continue
		}
		path, owner := name, ""
		switch {
		case parent == "":
			rootName = name
		case parent == ".":
			owner = rootName
		default:
			path, owner = parent+"/"+name, strings.TrimPrefix(rootName+"/"+parent, "/")
		}
		if len(syms) == 0 {
			path = "."
		}
		sym := parser.Symbol{
			Name: name, Kind: parser.KindScene, Module: mod, Owner: owner, Signature: s.attrs["type"],
			Content: s.body, StartLine: s.start, EndLine: s.end, Exported: true,
			Metadata: map[string]string{"type": s.attrs["type"]},
		}
		if parent != "" {
			sym.Metadata["parent"] = parent
		}
		if inst := extPath(s.attrs["instance"]); inst != "" {
			sym.Metadata["instance"] = inst
		}
		if p := extPath(s.script); p != "" {
			m := resModule(p, mod)
			scriptMod[path] = m
			sym.Relations = []parser.Relation{{Kind: parser.RelUsesType, TargetName: stem(p), TargetQualifiedName: m, Line: s.scriptLine}}
		}
		byPath[path] = len(syms)
		syms = append(syms, sym)
	}
	if len(syms) == 0 {
		return nil
	}
	if truncated {
		syms[len(syms)-1].Metadata["truncated"] = "true"
	}
	for _, s := range secs {
		if s.kind != "connection" {
			continue
		}
		i, ok := byPath[s.attrs["from"]]
		method := s.attrs["method"]
		if !ok || method == "" {
			continue
		}
		r := parser.Relation{Kind: parser.RelCall, TargetName: method, Line: s.start}
		if m, ok := scriptMod[s.attrs["to"]]; ok {
			r.TargetQualifiedName = m + "." + method
		}
		syms[i].Relations = append(syms[i].Relations, r)
	}
	return syms
}

// sections splits the file at "[...]" header lines. Only the script
// property is read from the lines under a header.
func sections(src []byte) []section {
	var out []section
	lines := strings.Split(string(src), "\n")
	flush := func(s *section) {
		if s == nil {
			return
		}
		body := strings.Join(lines[s.start-1:s.end], "\n")
		if len(body) > maxContent {
			body = body[:maxContent]
		}
		s.body = body
		out = append(out, *s)
	}
	var cur *section
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush(cur)
			kind, attrs := header(line[1 : len(line)-1])
			cur = &section{kind: kind, attrs: attrs, start: i + 1, end: i + 1}
			continue
		}
		if cur == nil || line == "" {
			continue
		}
		cur.end = i + 1
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "script" {
			cur.script, cur.scriptLine = strings.TrimSpace(v), i+1
		}
	}
	flush(cur)
	return out
}

// header parses `node name="X" parent="." groups=["a", "b"]` into its kind
// and attributes. Quoted values are unquoted; bracketed and parenthesised
// values are kept whole.
func header(s string) (string, map[string]string) {
	kind, rest, _ := strings.Cut(s, " ")
	attrs := map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; rest = strings.TrimSpace(rest) {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		var val string
		val, rest = value(after)
		attrs[strings.TrimSpace(key)] = val
	}
	return kind, attrs
}

// value reads one attribute value from the start of s and returns it with
// the remainder of s.
func value(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				return s[1:i], s[i+1:]
			}
		}
		return s[1:], ""
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case ' ', '\t':
			if depth <= 0 {
				return s[:i], s[i:]
			}
		}
	}
	return s, ""
}

// extResourceID extracts the id from `ExtResource("1_x")` (Godot 4) or
// `ExtResource( 1 )` (Godot 3); "" for any other value.
func extResourceID(v string) string {
	inner, ok := strings.CutPrefix(v, "ExtResource")
	if !ok {
		return ""
	}
	inner = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(inner), "("), ")")
	return strings.Trim(strings.TrimSpace(inner), `"`)
}
