package docs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// Data files share one shape: top-level keys become chunks, their direct
// children become chunks owned by the key, deeper levels stay inside the
// parent's content. Arrays are never expanded. Values longer than
// maxValueLines keep their real line span but a capped Content.

func (d *doc) value(owner, name string, start, end int) parser.Symbol {
	end = d.trimEnd(start, end)
	s := d.chunk(owner, name, start, end)
	// shortcut: a one-line YAML flow mapping still gives every key up to
	// maxValueBytes of the shared line; slice by column if that shows up.
	s.Content = capLines(s.Content, maxValueLines)
	return s
}

// ---- JSON ----

type jsonEntry struct {
	key      string
	keyStart int // byte offset of the key in the file
	valStart int // byte offset of the value in the file
	end      int // byte offset just past the value
	raw      json.RawMessage
}

// jsonObject lists the members of the object src starts with. base is the
// offset of src in the file. A non-object value yields nil entries.
func jsonObject(src []byte, base int) ([]jsonEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('{') {
		return nil, nil
	}
	var out []jsonEntry
	for dec.More() {
		// Only whitespace and the member comma sit between the previous
		// token and the key, so the key starts at the first other byte.
		keyStart := int(dec.InputOffset())
		for keyStart < len(src) && (src[keyStart] == ',' || src[keyStart] == ' ' || src[keyStart] == '\t' || src[keyStart] == '\n' || src[keyStart] == '\r') {
			keyStart++
		}
		t, err := dec.Token()
		if err != nil {
			return out, err
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, err
		}
		end := int(dec.InputOffset())
		out = append(out, jsonEntry{key: key, keyStart: base + keyStart, valStart: base + end - len(raw), end: base + end, raw: raw})
	}
	return out, nil
}

// json emits one chunk per top-level member and one per member of a nested
// object. Invalid JSON falls back to line windows so the file stays searchable.
func (d *doc) json() []parser.Symbol {
	entries, err := jsonObject(d.f.Source, 0)
	if err != nil || len(entries) == 0 {
		return d.plain()
	}
	nl := newlines(d.f.Source)
	var out []parser.Symbol
	for _, e := range entries {
		if len(out) >= maxSymbols {
			break
		}
		out = append(out, d.jsonValue("", e, nl))
		if len(e.raw) > 0 && e.raw[0] == '{' {
			kids, _ := jsonObject(e.raw, e.valStart)
			for _, k := range kids {
				out = append(out, d.jsonValue(e.key, k, nl))
			}
		}
	}
	return out
}

// jsonValue spans the key's line to the value's last line; Content is the
// exact `"key": value` source (not the line span, which is the whole file
// for minified JSON) capped at maxValueLines lines.
func (d *doc) jsonValue(owner string, e jsonEntry, nl []int) parser.Symbol {
	s := d.chunk(owner, e.key, lineOf(nl, e.keyStart), lineOf(nl, e.end-1))
	s.Content = capLines(string(d.f.Source[e.keyStart:e.end]), maxValueLines)
	return s
}

// capLines keeps the first n lines of s, and at most maxValueBytes.
func capLines(s string, n int) string {
	s = clip(s, maxValueBytes)
	for i, c := range s {
		if c == '\n' {
			if n--; n == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// ---- YAML ----

// yaml decodes every document into a yaml.Node tree and emits the mapping
// keys. A key's span ends on the line before the next key (or the next
// document's "---" line), since yaml.v3 only records start lines.
func (d *doc) yaml() []parser.Symbol {
	dec := yaml.NewDecoder(bytes.NewReader(d.f.Source))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(docs) == 0 {
				return d.plain()
			}
			break
		}
		if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
			docs = append(docs, &n)
		}
	}
	var out []parser.Symbol
	for i, root := range docs {
		limit := len(d.lines) // the next DocumentNode starts on its "---" line
		if i+1 < len(docs) {
			limit = docs[i+1].Line - 1
		}
		out = append(out, d.yamlMap("", root.Content[0], limit)...)
	}
	if len(out) == 0 {
		return d.plain()
	}
	return out
}

func (d *doc) yamlMap(owner string, m *yaml.Node, limit int) []parser.Symbol {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	var out []parser.Symbol
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		end := limit
		if i+2 < len(m.Content) {
			end = m.Content[i+2].Line - 1
		}
		out = append(out, d.value(owner, k.Value, k.Line, end))
		if owner == "" {
			out = append(out, d.yamlMap(k.Value, v, end)...)
		}
	}
	return out
}

// ---- TOML ----

var (
	tomlTable = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	tomlKey   = regexp.MustCompile(`^\s*("[^"]*"|'[^']*'|[A-Za-z0-9_.-]+)\s*=`)
)

// toml is a line scanner: [table] and [[table]] headers become chunks, keys
// become chunks owned by the current table. A key spans to the line before
// the next key or header, so multi-line values stay attached.
// shortcut: a multi-line string whose line looks like `k = v` is read as a key; a real tokenizer fixes it if that shows up.
func (d *doc) toml() []parser.Symbol {
	type item struct {
		name, owner string
		table       bool
		start, end  int
	}
	var items []item
	table := ""
	for i, line := range d.lines {
		if len(items) > maxSymbols { // the extra item only bounds the last emitted one
			break
		}
		if m := tomlTable.FindStringSubmatch(line); m != nil {
			table = m[1]
			items = append(items, item{name: table, table: true, start: i + 1})
		} else if m := tomlKey.FindStringSubmatch(line); m != nil {
			items = append(items, item{name: strings.Trim(m[1], `"'`), owner: table, start: i + 1})
		}
	}
	if len(items) == 0 {
		return d.plain()
	}
	var out []parser.Symbol
	for i, it := range items {
		end := len(d.lines)
		for _, next := range items[i+1:] {
			if it.table && !next.table {
				continue
			}
			end = next.start - 1
			break
		}
		out = append(out, d.value(it.owner, it.name, it.start, end))
	}
	return out
}
