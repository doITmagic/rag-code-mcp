package docs

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// Independent verification probes (kept on purpose).

func TestVerifyMarkdownLinksAndHeadings(t *testing.T) {
	src := "\n\n# Guide #\n\nSee [arch](../ARCH.md), [notes](sub/notes.txt), [cfg](config.yaml#x), [out](../../out.md), [code](main.go), [titled](./a.md \"T\").\n\n" +
		"Setext\n------\n\nbody\n\n~~~\n```\n# fenced-in-tilde\n```\n~~~\n\n## Sub.Section\n\ntext\n"
	syms := analyze(t, "docs/dev/guide.md", src)
	t.Logf("names=%v", names(syms))
	for _, s := range syms {
		t.Logf("%q owner=%q qn=%q lines=%d-%d sig=%q", s.Name, s.Owner, s.QualifiedName, s.StartLine, s.EndLine, s.Signature)
		for _, r := range s.Relations {
			t.Logf("   rel %q -> %q line %d", r.TargetName, r.TargetQualifiedName, r.Line)
		}
	}
	g := byName(syms, "Guide")
	if g == nil {
		g = byName(syms, "Guide #")
	}
	require.NotNil(t, g)
	assert.Equal(t, "docs/dev/guide", g.Module)
	got := map[string]string{}
	for _, r := range g.Relations {
		got[r.TargetName] = r.TargetQualifiedName
	}
	assert.Equal(t, "docs/ARCH", got["../ARCH.md"])
	assert.Equal(t, "docs/dev/sub/notes", got["sub/notes.txt"])
	assert.Equal(t, "docs/dev/config", got["config.yaml#x"])
	assert.Equal(t, "out", got["../../out.md"], "docs/dev/../../out.md is <root>/out.md")
	assert.Equal(t, "", got["main.go"])
	assert.Equal(t, "docs/dev/a", got["./a.md"])
	for _, s := range syms {
		assert.NotContains(t, s.Name, "fenced-in-tilde")
	}
}

func TestVerifyTOMLNestedArray(t *testing.T) {
	src := "[tool]\npoints = [\n  [1, 2],\n  [3, 4],\n]\nname = \"x\"\n"
	syms := analyze(t, "pyproject.toml", src)
	for _, s := range syms {
		t.Logf("%q owner=%q qn=%q lines=%d-%d", s.Name, s.Owner, s.QualifiedName, s.StartLine, s.EndLine)
	}
	n := byName(syms, "name")
	require.NotNil(t, n)
	assert.Equal(t, "tool", n.Owner, "name must stay owned by [tool]")
	assert.Nil(t, byName(syms, "1, 2"), "array row read as table")
}

func TestVerifyJSONMinifiedContentSize(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 500; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\"k%d\":\"%s\"", i, strings.Repeat("v", 2000))
	}
	b.WriteString("}")
	src := b.String()
	syms := analyze(t, "min.json", src)
	total := 0
	for _, s := range syms {
		total += len(s.Content)
	}
	t.Logf("file=%d bytes, symbols=%d, total content=%d bytes, per symbol=%d", len(src), len(syms), total, len(syms[0].Content))
	assert.Less(t, len(syms[0].Content), len(src), "Content of one key should not be the whole file")
}

func TestVerifyJSONKeyOnOwnLine(t *testing.T) {
	src := "{\n  \"scripts\":\n  {\n    \"a\": 1\n  }\n}\n"
	syms := analyze(t, "x.json", src)
	s := byName(syms, "scripts")
	require.NotNil(t, s)
	t.Logf("scripts start=%d end=%d sig=%q", s.StartLine, s.EndLine, s.Signature)
	assert.Equal(t, 2, s.StartLine, "StartLine should be the key line")
}

func TestVerifyYAMLCommentAfterDocSep(t *testing.T) {
	src := "a: 1\n---\n# c\nb: 2\n"
	syms := analyze(t, "y.yaml", src)
	for _, s := range syms {
		t.Logf("%q %d-%d content=%q", s.Name, s.StartLine, s.EndLine, s.Content)
	}
	a := byName(syms, "a")
	require.NotNil(t, a)
	assert.Equal(t, 1, a.EndLine)
}

func TestVerifyTiming(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "## H%d\n\n- item [l](./x%d.md)\n> quote\n\n```\ncode\n```\n\n", i, i)
	}
	for _, tc := range []struct{ path, src string }{
		{"big.md", b.String()},
		{"deep.md", strings.Repeat("> ", 5000) + "x\n"},
		{"list.md", strings.Repeat("  ", 3000) + "- x\n"},
		{"big.json", "{" + strings.Repeat("\"a\":{\"b\":1},", 100000) + "\"z\":0}"},
		{"big.yaml", strings.Repeat("k: v\n", 200000)},
		{"big.toml", strings.Repeat("k = 1\n", 200000)},
		{"big.txt", strings.Repeat("x\n", 500000)},
		{"one.md", "#" + strings.Repeat(" a", 500000) + "\n"},
	} {
		start := time.Now()
		syms, err := analyzer{}.Analyze(parser.File{Path: root + "/" + tc.path, Root: root, Source: []byte(tc.src), Language: "docs"})
		d := time.Since(start)
		t.Logf("%s: %d bytes -> %d syms in %s", tc.path, len(tc.src), len(syms), d)
		require.NoError(t, err)
		// A hang guard, not a benchmark: these inputs are 4x the 256 KiB the index
		// reads, and -race on a Windows runner parsed big.yaml in 5.1 s.
		assert.Less(t, d, 20*time.Second, tc.path)
		assert.LessOrEqual(t, len(syms), maxSymbols)
	}
}

// Every extension on empty, one-byte, binary and truncated input: no panic,
// no error, every symbol well-formed.
func TestVerifyRobustness(t *testing.T) {
	exts := analyzer{}.Extensions()
	inputs := map[string]string{
		"empty": "", "byte": "#", "brace": "{", "bracket": "[", "nul": "\x00", "binary": "\xff\xfe\x00\x01\x80# {[\n\x00:\n",
		"truncated-json": "{\"a\": {\"b\": [1, 2", "truncated-yaml": "a:\n  b: [1,\n", "truncated-toml": "[t\nk = \"", "deep-json": strings.Repeat("{\"a\":", 5000),
		"crlf-only": "\r\n\r\n", "fence-only": "```\n# x\n", "utf8": "# \u00e9t\u00e9 \u4e2d\n\ntext \U0001F600\n",
	}
	for _, ext := range exts {
		for name, src := range inputs {
			t.Run(ext+"/"+name, func(t *testing.T) {
				done := make(chan []parser.Symbol, 1)
				go func() {
					syms, err := analyzer{}.Analyze(parser.File{Path: root + "/f" + ext, Root: root, Source: []byte(src), Language: "docs"})
					assert.NoError(t, err)
					done <- syms
				}()
				select {
				case syms := <-done:
					for _, s := range syms {
						assert.Equal(t, parser.KindChunk, s.Kind)
						assert.NotEmpty(t, s.QualifiedName)
						assert.NotEmpty(t, s.ID)
						assert.GreaterOrEqual(t, s.StartLine, 1)
						assert.GreaterOrEqual(t, s.EndLine, s.StartLine)
						assert.LessOrEqual(t, s.EndLine, strings.Count(src, "\n")+1)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("hang")
				}
			})
		}
	}
	// nil Source and empty Root must not panic.
	syms, err := analyzer{}.Analyze(parser.File{Path: "/x/README.md"})
	require.NoError(t, err)
	assert.Empty(t, syms)
}

// A CHANGELOG-style file (many sibling headings) under the indexer's 4 MiB cap
// must stay well under the 5 s budget; the walk is quadratic in sibling count.
func TestVerifyChangelogWalk(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "## v1.%d\n\n- fixed thing %d\n- added thing\n\n", i, i)
	}
	start := time.Now()
	syms, err := analyzer{}.Analyze(parser.File{Path: root + "/CHANGELOG.md", Root: root, Source: []byte(b.String()), Language: "docs"})
	d := time.Since(start)
	require.NoError(t, err)
	t.Logf("%d bytes -> %d syms in %s", b.Len(), len(syms), d)
	assert.Less(t, d, 5*time.Second)
	assert.Equal(t, "v1.0", syms[0].Name)
	assert.Equal(t, "", syms[0].Owner)
}

// Data-file probes: spec says Content is the serialized value capped at 100
// lines, so a key never carries the whole file.
func TestVerifyDataEdges(t *testing.T) {
	// YAML: an empty complex key and a top-level scalar must not panic.
	for _, src := range []string{"? [a, b]\n: 1\n", "just a scalar\n", "&x a: *x\n", "a: &a [1]\nb: *a\n"} {
		syms, err := analyzer{}.Analyze(parser.File{Path: root + "/e.yaml", Root: root, Source: []byte(src), Language: "docs"})
		require.NoError(t, err, src)
		for _, s := range syms {
			assert.NotEmpty(t, s.QualifiedName, src)
		}
	}
	// TOML: a header followed by a comment, and dotted/quoted keys.
	syms := analyze(t, "c.toml", "[a.b] # c\nx.y = 1\n'q k' = 2\n")
	require.Equal(t, []string{"a.b", "x.y", "q k"}, names(syms))
	assert.Equal(t, "a.b", syms[1].Owner)
	// JSON: duplicate top-level keys stay distinct symbols (IDs differ by line).
	syms = analyze(t, "d.json", "{\n\"a\": 1,\n\"a\": 2\n}\n")
	require.Len(t, syms, 2)
	assert.NotEqual(t, syms[0].ID, syms[1].ID)
}

// ---- second verification pass ----

func TestVerify2MarkdownOwnersAndSpans(t *testing.T) {
	src := "# Top\r\nintro\r\n## A\r\na-text\r\n#### A1\r\na1\r\n## B\r\nb\r\n# Top2\r\n## C\r\nc [l](./z.md)\r\n\r\n    # indented code\r\n````\r\n```\r\n# not a heading\r\n```\r\n````\r\n#hashtag\r\n"
	syms := analyze(t, "README.md", src)
	require.Equal(t, []string{"Top", "A", "A1", "B", "Top2", "C"}, names(syms))
	want := []struct {
		name, owner, qn string
		start, end      int
	}{
		{"Top", "", "README.Top", 1, 8},
		{"A", "Top", "README.Top.A", 3, 6},
		{"A1", "A", "README.A.A1", 5, 6},
		{"B", "Top", "README.Top.B", 7, 8},
		{"Top2", "", "README.Top2", 9, 19},
		{"C", "Top2", "README.Top2.C", 10, 19},
	}
	for i, w := range want {
		s := syms[i]
		assert.Equal(t, w.owner, s.Owner, w.name)
		assert.Equal(t, w.qn, s.QualifiedName, w.name)
		assert.Equal(t, w.start, s.StartLine, w.name)
		assert.Equal(t, w.end, s.EndLine, w.name)
		assert.Equal(t, "readme", s.Metadata["doc"], w.name)
		assert.NotContains(t, s.Name, "\r")
		assert.NotContains(t, s.Signature, "\r")
	}
	c := byName(syms, "C")
	require.Len(t, c.Relations, 1)
	assert.Equal(t, parser.RelImports, c.Relations[0].Kind)
	assert.Equal(t, "z", c.Relations[0].TargetQualifiedName)
	assert.Equal(t, 11, c.Relations[0].Line)
}

func TestVerify2JSONNesting(t *testing.T) {
	src := "{\n  \"name\": \"x\",\n  \"deps\": {\n    \"a\": \"1\",\n    \"b\": {\n      \"c\": 2\n    }\n  },\n  \"arr\": [\n    {\"k\": 1}\n  ]\n}\n"
	syms := analyze(t, "cfg/pkg.json", src)
	require.Equal(t, []string{"name", "deps", "a", "b", "arr"}, names(syms))
	want := map[string][3]interface{}{"name": {"", 2, 2}, "deps": {"", 3, 8}, "a": {"deps", 4, 4}, "b": {"deps", 5, 7}, "arr": {"", 9, 11}}
	for _, s := range syms {
		w := want[s.Name]
		assert.Equal(t, w[0], s.Owner, s.Name)
		assert.Equal(t, w[1], s.StartLine, s.Name)
		assert.Equal(t, w[2], s.EndLine, s.Name)
		assert.Equal(t, "cfg/pkg", s.Module)
	}
	assert.Equal(t, "\"deps\": {\n    \"a\": \"1\",\n    \"b\": {\n      \"c\": 2\n    }\n  }", byName(syms, "deps").Content)
	assert.Equal(t, "cfg/pkg.deps.b", byName(syms, "b").QualifiedName)
	// top-level array and bare scalar fall back to line chunks
	for _, s := range []string{"[{\"a\":1}]\n", "42\n"} {
		syms := analyze(t, "a.json", s)
		require.Len(t, syms, 1)
		assert.Equal(t, "a", syms[0].Name)
	}
}

func TestVerify2YAMLNesting(t *testing.T) {
	src := "name: x\ndeps:\n  a: \"1\"\n  b:\n    c: 2\nscript: |\n  line1\n  line2\nlist:\n  - k: 1\n"
	syms := analyze(t, "ci.yml", src)
	require.Equal(t, []string{"name", "deps", "a", "b", "script", "list"}, names(syms))
	want := map[string][3]interface{}{"name": {"", 1, 1}, "deps": {"", 2, 5}, "a": {"deps", 3, 3}, "b": {"deps", 4, 5}, "script": {"", 6, 8}, "list": {"", 9, 10}}
	for _, s := range syms {
		w := want[s.Name]
		assert.Equal(t, w[0], s.Owner, s.Name)
		assert.Equal(t, w[1], s.StartLine, s.Name)
		assert.Equal(t, w[2], s.EndLine, s.Name)
	}
	assert.Equal(t, "script: |\n  line1\n  line2", byName(syms, "script").Content)
}

func TestVerify2TOMLTables(t *testing.T) {
	src := "title = \"t\"\n\n[owner]\nname = \"x\"\ndob = 1979\n\n[servers.alpha]\nip = \"1\"\n\n[[products]]\nname = \"p1\"\n\n[[products]]\nname = \"p2\"\n"
	syms := analyze(t, "Cargo.toml", src)
	require.Equal(t, []string{"title", "owner", "name", "dob", "servers.alpha", "ip", "products", "name", "products", "name"}, names(syms))
	assert.Equal(t, [2]int{1, 1}, [2]int{syms[0].StartLine, syms[0].EndLine})
	assert.Equal(t, [2]int{3, 5}, [2]int{syms[1].StartLine, syms[1].EndLine})
	assert.Equal(t, "owner", syms[2].Owner)
	assert.Equal(t, [2]int{4, 4}, [2]int{syms[2].StartLine, syms[2].EndLine})
	assert.Equal(t, "servers.alpha", syms[5].Owner)
	assert.Equal(t, "Cargo.servers.alpha.ip", syms[5].QualifiedName)
	assert.Equal(t, "products", syms[7].Owner)
	assert.Equal(t, "products", syms[9].Owner)
	assert.NotEqual(t, syms[7].ID, syms[9].ID)
	// known ceiling of the line scanner: a nested array row with no trailing comma
	syms = analyze(t, "p.toml", "[tool]\npoints = [\n  [1, 2],\n  [3, 4]\n]\nname = \"x\"\n")
	t.Logf("no-trailing-comma array row: names=%v owner(name)=%q", names(syms), byName(syms, "name").Owner)
}

func TestVerify2RSTAndModule(t *testing.T) {
	src := "preamble\n\n=====\nTitle\n=====\nintro\n\nSub\n---\nbody\n"
	syms := analyze(t, "doc/index.rst", src)
	require.Equal(t, []string{"index", "Title", "Sub"}, names(syms))
	assert.Equal(t, [2]int{3, 6}, [2]int{syms[1].StartLine, syms[1].EndLine}) // trailing blank line trimmed
	assert.Equal(t, [2]int{8, 10}, [2]int{syms[2].StartLine, syms[2].EndLine})
	assert.Equal(t, "doc/index", syms[1].Module)
	// outside root / empty root -> stem
	for _, f := range []parser.File{{Path: "/elsewhere/x.md", Root: root}, {Path: "/ws/sub/x.md"}} {
		f.Source = []byte("# H\n")
		syms, err := analyzer{}.Analyze(f)
		require.NoError(t, err)
		require.Len(t, syms, 1)
		assert.Equal(t, "x", syms[0].Module)
	}
	// uppercase extension routes through the registry
	a := parser.ForFile("/ws/NOTES.MD")
	require.NotNil(t, a)
	assert.Equal(t, "docs", a.Language())
	syms, err := parser.Analyze(parser.File{Path: "/ws/NOTES.MD", Root: root, Source: []byte("# H\nx\n")})
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "H", syms[0].Name)
}

func TestVerify2DeterministicAndCap(t *testing.T) {
	f := parser.File{Path: root + "/d.md", Root: root, Source: []byte(readme), Language: "docs"}
	a, _ := analyzer{}.Analyze(f)
	b, _ := analyzer{}.Analyze(f)
	assert.Equal(t, a, b)
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "# H%d\n## S%d\n", i, i)
	}
	syms := analyze(t, "cap.md", sb.String())
	assert.Equal(t, maxSymbols, len(syms))
	sb.Reset()
	sb.WriteString("{")
	for i := 0; i < 300; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, "\"k%d\":{\"a\":1,\"b\":2}", i)
	}
	sb.WriteString("}")
	assert.Equal(t, maxSymbols, len(analyze(t, "cap.json", sb.String())))
	txt := strings.Repeat("line\n", 2000)
	syms = analyze(t, "big.txt", txt)
	require.Len(t, syms, 10)
	assert.Equal(t, "big (1/10)", syms[0].Name)
	assert.Equal(t, [2]int{1, 200}, [2]int{syms[0].StartLine, syms[0].EndLine})
	assert.Equal(t, [2]int{1801, 2000}, [2]int{syms[9].StartLine, syms[9].EndLine})
	syms = analyze(t, "one.txt", "hello\n")
	require.Len(t, syms, 1)
	assert.Equal(t, "one", syms[0].Name)
}

// Minified JSON: every key is on line 1, so a line-based signature repeated
// the whole file per key (a 540x payload in the soak).
func TestMinifiedJSONStaysSmall(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"big":"` + strings.Repeat("é", 100_000) + `"`)
	for i := range 300 {
		fmt.Fprintf(&b, `,"k%d":{"v":"%s"}`, i, strings.Repeat("x", 100))
	}
	b.WriteString("}")
	src := b.String()
	root := t.TempDir()
	syms, err := analyzer{}.Analyze(parser.File{Path: root + "/min.json", Root: root, Source: []byte(src), Language: "docs"})
	require.NoError(t, err)
	require.Greater(t, len(syms), 300)
	total := 0
	for _, s := range syms {
		assert.LessOrEqual(t, len(s.Signature), maxSignature, s.Name)
		assert.LessOrEqual(t, len(s.Content), maxValueBytes, s.Name)
		assert.True(t, utf8.ValidString(s.Signature) && utf8.ValidString(s.Content), s.Name)
		total += len(s.Signature) + len(s.Content)
	}
	assert.Less(t, total, 2*len(src), "payload stays in proportion to the file")
}

// One-line YAML repeats the line in every key's content; a split Markdown
// section reassigns the heading as every part's signature.
func TestYAMLAndSplitSectionsStaySmall(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	b.WriteString("{")
	for i := range 300 {
		fmt.Fprintf(&b, "k%d: %s, ", i, strings.Repeat("x", 100))
	}
	b.WriteString("z: 1}\n")
	src := b.String()
	syms, err := analyzer{}.Analyze(parser.File{Path: root + "/flow.yaml", Root: root, Source: []byte(src), Language: "docs"})
	require.NoError(t, err)
	require.Greater(t, len(syms), 300)
	for _, s := range syms {
		assert.LessOrEqual(t, len(s.Content), maxValueBytes, s.Name)
		assert.LessOrEqual(t, len(s.Signature), maxSignature, s.Name)
	}

	md := "# " + strings.Repeat("h", 300) + "\n" + strings.Repeat("line\n", 900)
	syms, err = analyzer{}.Analyze(parser.File{Path: root + "/long.md", Root: root, Source: []byte(md), Language: "docs"})
	require.NoError(t, err)
	require.Greater(t, len(syms), 1)
	for _, s := range syms {
		assert.LessOrEqual(t, len(s.Signature), maxSignature, s.Name)
	}
}
