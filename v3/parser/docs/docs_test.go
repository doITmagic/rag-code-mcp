package docs

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const root = "/ws"

func analyze(t *testing.T, rel, src string) []parser.Symbol {
	t.Helper()
	syms, err := analyzer{}.Analyze(parser.File{Path: root + "/" + rel, Root: root, Source: []byte(src), Language: "docs"})
	require.NoError(t, err)
	return syms
}

func byName(syms []parser.Symbol, name string) *parser.Symbol {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

func names(syms []parser.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Name
	}
	return out
}

const readme = `Intro paragraph before any heading.
See [guide](./guide.md) early.

# Project

Top text with [spec](specs/spec.md#rules) and [web](https://example.com/x.md).

## Install

` + "```bash\n# not a heading\necho hi\n```" + `

~~~
## also not a heading
~~~

## Usage

Usage text [parent](../outside.md) and [img](./pic.png) and [abs](/etc/x.md).

### Advanced

Deep text.

# Second

Last [anchor](#install) line.
`

func TestMarkdownReadme(t *testing.T) {
	syms := analyze(t, "docs/README.md", readme)
	assert.Equal(t, []string{"README", "Project", "Install", "Usage", "Advanced", "Second"}, names(syms))
	for _, s := range syms {
		assert.Equal(t, parser.KindChunk, s.Kind, s.Name)
		assert.True(t, s.Exported, s.Name)
		assert.Equal(t, "docs/README", s.Module, s.Name)
		assert.Equal(t, "docs", s.Language, s.Name)
		assert.Equal(t, "readme", s.Metadata["doc"], s.Name)
		assert.Equal(t, root+"/docs/README.md", s.FilePath)
		assert.NotEmpty(t, s.ID)
	}
	tests := []struct {
		name, owner, qualified, sig string
		start, end                  int
		targets                     map[string]string // TargetName -> TargetQualifiedName
	}{
		{"README", "", "docs/README.README", "Intro paragraph before any heading.", 1, 2, map[string]string{"./guide.md": "docs/guide"}},
		{"Project", "", "docs/README.Project", "# Project", 4, 25, map[string]string{"specs/spec.md#rules": "docs/specs/spec"}},
		{"Install", "Project", "docs/README.Project.Install", "## Install", 8, 17, nil},
		{"Usage", "Project", "docs/README.Project.Usage", "## Usage", 19, 25, map[string]string{"../outside.md": "outside", "./pic.png": ""}},
		{"Advanced", "Usage", "docs/README.Usage.Advanced", "### Advanced", 23, 25, nil},
		{"Second", "", "docs/README.Second", "# Second", 27, 29, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := byName(syms, tc.name)
			require.NotNil(t, s)
			assert.Equal(t, tc.owner, s.Owner)
			assert.Equal(t, tc.qualified, s.QualifiedName)
			assert.Equal(t, tc.sig, s.Signature)
			assert.Equal(t, tc.start, s.StartLine, "start")
			assert.Equal(t, tc.end, s.EndLine, "end")
			assert.Equal(t, s.Content, strings.Join(strings.Split(readme, "\n")[tc.start-1:tc.end], "\n"))
			got := map[string]string{}
			for _, r := range s.Relations {
				assert.Equal(t, parser.RelImports, r.Kind)
				assert.Contains(t, strings.Split(readme, "\n")[r.Line-1], r.TargetName, "relation line")
				got[r.TargetName] = r.TargetQualifiedName
			}
			want := tc.targets
			if want == nil {
				want = map[string]string{}
			}
			for k, v := range want {
				assert.Contains(t, got, k)
				assert.Equal(t, v, got[k], k)
			}
			for k := range got {
				if _, ok := want[k]; !ok && (strings.HasPrefix(k, "http") || strings.HasPrefix(k, "#") || strings.HasPrefix(k, "/") || strings.HasSuffix(k, ".png")) {
					assert.Equal(t, "", got[k], k)
				} else if !ok {
					t.Errorf("unexpected link %q", k)
				}
			}
		})
	}
	// Links in a subsection are attached to the subsection only.
	assert.Empty(t, byName(syms, "Install").Relations)
	for _, r := range byName(syms, "Project").Relations {
		assert.NotEqual(t, "./guide.md", r.TargetName)
	}
	// Anchor-only links never become relations.
	assert.Empty(t, byName(syms, "Second").Relations)
}

func TestMarkdownLongSectionSplit(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Big\n")
	for i := 0; i < 900; i++ {
		fmt.Fprintf(&b, "line %d [l%d](./f%d.md)\n", i, i, i)
	}
	b.WriteString("# Small\ntext\n")
	syms := analyze(t, "big.md", b.String())
	require.Equal(t, []string{"Big (1/3)", "Big (2/3)", "Big (3/3)", "Small"}, names(syms))
	assert.Equal(t, "big", syms[0].Module)
	assert.Nil(t, syms[0].Metadata)
	ranges := [][2]int{{1, 400}, {401, 800}, {801, 901}}
	for i, r := range ranges {
		assert.Equal(t, r[0], syms[i].StartLine)
		assert.Equal(t, r[1], syms[i].EndLine)
		assert.Equal(t, "# Big", syms[i].Signature)
		assert.Equal(t, "big.Big ("+fmt.Sprint(i+1)+"/3)", syms[i].QualifiedName)
		for _, rel := range syms[i].Relations {
			assert.GreaterOrEqual(t, rel.Line, r[0])
			assert.LessOrEqual(t, rel.Line, r[1])
		}
	}
	assert.Len(t, syms[0].Relations, 399)
	assert.Len(t, syms[1].Relations, 400)
	assert.Len(t, syms[2].Relations, 101)
	assert.Equal(t, "f0", syms[0].Relations[0].TargetQualifiedName)
}

func TestMarkdownNoHeadings(t *testing.T) {
	src := strings.Repeat("prose\n", 250)
	syms := analyze(t, "notes.markdown", src)
	require.Len(t, syms, 2)
	assert.Equal(t, "notes (1/2)", syms[0].Name)
	assert.Equal(t, 1, syms[0].StartLine)
	assert.Equal(t, 200, syms[0].EndLine)
	assert.Equal(t, 201, syms[1].StartLine)
	assert.Equal(t, 250, syms[1].EndLine)
	assert.Equal(t, "notes.notes (1/2)", syms[0].QualifiedName)
}

const sampleJSON = `{
  "name": "demo",
  "scripts": {
    "build": "go build",
    "test": {
      "deep": true
    }
  },
  "list": [1, 2,
    3],
  "empty": {}
}
`

func TestJSON(t *testing.T) {
	syms := analyze(t, "package.json", sampleJSON)
	assert.Equal(t, []string{"name", "scripts", "build", "test", "list", "empty"}, names(syms))
	tests := []struct {
		name, owner string
		start, end  int
		content     string
	}{
		{"name", "", 2, 2, `"name": "demo"`},
		{"scripts", "", 3, 8, ""},
		{"build", "scripts", 4, 4, `"build": "go build"`},
		{"test", "scripts", 5, 7, "\"test\": {\n      \"deep\": true\n    }"},
		{"list", "", 9, 10, "\"list\": [1, 2,\n    3]"},
		{"empty", "", 11, 11, `"empty": {}`},
	}
	for _, tc := range tests {
		s := byName(syms, tc.name)
		require.NotNil(t, s, tc.name)
		assert.Equal(t, tc.owner, s.Owner, tc.name)
		assert.Equal(t, tc.start, s.StartLine, tc.name)
		assert.Equal(t, tc.end, s.EndLine, tc.name)
		assert.Equal(t, parser.Qualify("package", tc.owner, tc.name), s.QualifiedName)
		if tc.content != "" {
			assert.Equal(t, tc.content, s.Content, tc.name)
		}
	}
	// No symbol for "deep": only one nested level is expanded.
	assert.Nil(t, byName(syms, "deep"))
}

func TestJSONInvalidAndNonObject(t *testing.T) {
	syms := analyze(t, "bad.json", "{\n  \"a\": 1,\n  \"b\": \n")
	require.Len(t, syms, 1)
	assert.Equal(t, "bad", syms[0].Name)
	assert.Equal(t, 1, syms[0].StartLine)
	assert.Equal(t, 3, syms[0].EndLine)

	syms = analyze(t, "arr.json", "[1,\n2]\n")
	require.Len(t, syms, 1)
	assert.Equal(t, "arr", syms[0].Name)

	assert.Empty(t, analyze(t, "empty.json", "   \n"))
}

func TestJSONLongValueCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("{\"big\": {\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "  \"k%d\": %d,\n", i, i)
	}
	b.WriteString("  \"last\": 0\n}}\n")
	syms := analyze(t, "cfg.json", b.String())
	s := byName(syms, "big")
	require.NotNil(t, s)
	assert.Equal(t, 1, s.StartLine)
	assert.Equal(t, 303, s.EndLine)
	assert.Len(t, strings.Split(s.Content, "\n"), maxValueLines)
	assert.Len(t, syms, 302)
}

const sampleYAML = `# comment
name: demo
build:
  cmd: go build
  env:
    deep: 1
  steps:
    - one
    - two
list:
  - a
  - b
---
second: doc
`

func TestYAML(t *testing.T) {
	syms := analyze(t, "ci/config.yml", sampleYAML)
	assert.Equal(t, []string{"name", "build", "cmd", "env", "steps", "list", "second"}, names(syms))
	tests := []struct {
		name, owner string
		start, end  int
	}{
		{"name", "", 2, 2},
		{"build", "", 3, 9},
		{"cmd", "build", 4, 4},
		{"env", "build", 5, 6},
		{"steps", "build", 7, 9},
		{"list", "", 10, 12},
		{"second", "", 14, 14},
	}
	for _, tc := range tests {
		s := byName(syms, tc.name)
		require.NotNil(t, s, tc.name)
		assert.Equal(t, tc.owner, s.Owner, tc.name)
		assert.Equal(t, tc.start, s.StartLine, tc.name)
		assert.Equal(t, tc.end, s.EndLine, tc.name)
		assert.Equal(t, parser.Qualify("ci/config", tc.owner, tc.name), s.QualifiedName)
		assert.Equal(t, strings.Join(strings.Split(sampleYAML, "\n")[tc.start-1:tc.end], "\n"), s.Content)
	}
	assert.Equal(t, "name: demo", byName(syms, "name").Signature)
}

func TestYAMLInvalidFallsBack(t *testing.T) {
	syms := analyze(t, "bad.yaml", "a: [1,\nb: {\n")
	require.Len(t, syms, 1)
	assert.Equal(t, "bad", syms[0].Name)
	// A top-level list has no keys: line windows.
	syms = analyze(t, "list.yaml", "- a\n- b\n")
	require.Len(t, syms, 1)
	assert.Equal(t, "list", syms[0].Name)
}

const sampleTOML = `# top
title = "demo"
"quoted key" = 1

[server]
host = "localhost"
ports = [
  80,
  443,
]

[[products]]
name = "a"

[[products]]
name = "b"
`

func TestTOML(t *testing.T) {
	syms := analyze(t, "conf/app.toml", sampleTOML)
	assert.Equal(t, []string{"title", "quoted key", "server", "host", "ports", "products", "name", "products", "name"}, names(syms))
	tests := []struct {
		idx        int
		owner      string
		start, end int
	}{
		{0, "", 2, 2},
		{1, "", 3, 3},
		{2, "", 5, 10},
		{3, "server", 6, 6},
		{4, "server", 7, 10},
		{5, "", 12, 13},
		{6, "products", 13, 13},
		{7, "", 15, 16},
		{8, "products", 16, 16},
	}
	for _, tc := range tests {
		s := syms[tc.idx]
		assert.Equal(t, tc.owner, s.Owner, s.Name)
		assert.Equal(t, tc.start, s.StartLine, s.Name)
		assert.Equal(t, tc.end, s.EndLine, s.Name)
		assert.Equal(t, parser.Qualify("conf/app", tc.owner, s.Name), s.QualifiedName)
	}
	assert.Equal(t, "ports = [\n  80,\n  443,\n]", syms[4].Content)
	assert.NotEqual(t, syms[5].ID, syms[7].ID)
}

func TestPlainText2000Lines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	syms := analyze(t, "notes/log.txt", b.String())
	require.Len(t, syms, 10)
	for i, s := range syms {
		assert.Equal(t, fmt.Sprintf("log (%d/10)", i+1), s.Name)
		assert.Equal(t, "notes/log", s.Module)
		assert.Equal(t, i*200+1, s.StartLine)
		assert.Equal(t, i*200+200, s.EndLine)
		assert.Equal(t, parser.KindChunk, s.Kind)
		assert.True(t, strings.HasPrefix(s.Content, fmt.Sprintf("line %d\n", i*200+1)))
		assert.True(t, strings.HasSuffix(s.Content, fmt.Sprintf("line %d", i*200+200)))
	}
}

func TestRST(t *testing.T) {
	src := "preamble\n\n=====\nTitle\n=====\n\nbody\n\nSection\n-------\n\nmore\n---\nnot a title\n"
	syms := analyze(t, "doc.rst", src)
	require.Equal(t, []string{"doc", "Title", "Section"}, names(syms))
	assert.Equal(t, 1, syms[0].StartLine)
	assert.Equal(t, 1, syms[0].EndLine)
	assert.Equal(t, 3, syms[1].StartLine)
	assert.Equal(t, 7, syms[1].EndLine)
	assert.Equal(t, 9, syms[2].StartLine)
	assert.Equal(t, 14, syms[2].EndLine)
	assert.Equal(t, "doc.Section", syms[2].QualifiedName)

	syms = analyze(t, "flat.rst", "just\ntext\n")
	require.Len(t, syms, 1)
	assert.Equal(t, "flat", syms[0].Name)
}

func TestMalformedAndEdgeInputs(t *testing.T) {
	tests := []struct {
		name, path, src string
	}{
		{"empty markdown", "e.md", ""},
		{"binary markdown", "b.md", "\x00\xff\xfe# \x01\n```\n"},
		{"heading only", "h.md", "#"},
		{"unterminated fence", "f.md", "# A\n```\n# B\n"},
		{"crlf", "c.md", "# A\r\n\r\ntext\r\n## B\r\n"},
		{"no newline", "n.json", `{"a":1}`},
		{"garbage toml", "g.toml", "= = =\n[\n"},
		{"garbage yaml", "g.yaml", "\t\t: :\n"},
		{"outside root", "../x.md", "# Out\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			syms, err := analyzer{}.Analyze(parser.File{Path: root + "/" + tc.path, Root: root, Source: []byte(tc.src), Language: "docs"})
			require.NoError(t, err)
			for _, s := range syms {
				assert.NotEmpty(t, s.QualifiedName)
				assert.GreaterOrEqual(t, s.StartLine, 1)
				assert.GreaterOrEqual(t, s.EndLine, s.StartLine)
			}
		})
	}
	// An unterminated fence swallows the rest of the file: only A is a heading.
	syms := analyze(t, "f.md", "# A\n```\n# B\n")
	assert.Equal(t, []string{"A"}, names(syms))
	syms = analyze(t, "c.md", "# A\r\n\r\ntext\r\n## B\r\n")
	assert.Equal(t, []string{"A", "B"}, names(syms))
	assert.Equal(t, "A", syms[1].Owner)
	syms, _ = analyzer{}.Analyze(parser.File{Path: "/elsewhere/x.md", Root: root, Source: []byte("# Out\n")})
	require.Len(t, syms, 1)
	assert.Equal(t, "x.Out", syms[0].QualifiedName)
}

func TestSymbolCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&b, "k%d = %d\n", i, i)
	}
	assert.Len(t, analyze(t, "many.toml", b.String()), maxSymbols)
}

func TestRegistered(t *testing.T) {
	a := parser.ForFile("/ws/README.MD")
	require.NotNil(t, a)
	assert.Equal(t, "docs", a.Language())
	assert.NotNil(t, parser.ForFile("/ws/x.yml"))
	assert.Equal(t, "docs", parser.ForLanguage("docs").Language())
}

func TestConcurrentAnalyze(t *testing.T) {
	inputs := []struct{ path, src string }{
		{"README.md", readme}, {"p.json", sampleJSON}, {"c.yaml", sampleYAML}, {"a.toml", sampleTOML},
		{"t.txt", strings.Repeat("x\n", 500)}, {"d.rst", "T\n==\nb\n"},
	}
	want := make([][]parser.Symbol, len(inputs))
	for i, in := range inputs {
		want[i] = analyze(t, in.path, in.src)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, in := range inputs {
				got, err := analyzer{}.Analyze(parser.File{Path: root + "/" + in.path, Root: root, Source: []byte(in.src), Language: "docs"})
				assert.NoError(t, err)
				assert.Equal(t, want[i], got, "goroutine %d %s", g, in.path)
			}
		}(g)
	}
	wg.Wait()
}
