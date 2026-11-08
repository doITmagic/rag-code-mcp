package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxReadSize  = 8 << 20
	defaultLines = 120
	maxLineBytes = 2000 // per returned line: a minified file is one huge line
	maxSigBytes  = 200
)

// ReadInput is the input of rag_read_file_context.
type ReadInput struct {
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the project directory (required). Pass it on every call."`
	FilePath      string `json:"file_path" jsonschema:"File to read, absolute or relative to the root."`
	Symbol        string `json:"symbol,omitempty" jsonschema:"Return only this symbol (name or qualified name) with its relations."`
	StartLine     int    `json:"start_line,omitempty" jsonschema:"First line to return (1-based)."`
	EndLine       int    `json:"end_line,omitempty" jsonschema:"Last line to return; default start_line+119."`
}

// Outline is one symbol of the file outline.
type Outline struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Signature     string `json:"signature,omitempty"`
	Exported      bool   `json:"exported"`
}

func (t *tools) readFile(ctx context.Context, req *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, any, error) {
	w, err := t.resolveRoot(ctx, req, in.WorkspaceRoot)
	if err != nil {
		return fail(err.Error())
	}
	path, err := insideRoot(w.Root, in.FilePath)
	if err != nil {
		return fail(err.Error())
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fail(fmt.Sprintf("cannot read %s: %v", path, err))
	case !info.Mode().IsRegular():
		return fail(path + " is not a regular file")
	case in.StartLine < 0 || in.EndLine < 0:
		return fail(fmt.Sprintf("start_line and end_line are 1-based; got start_line %d, end_line %d", in.StartLine, in.EndLine))
	case in.EndLine > 0 && in.EndLine < max(in.StartLine, 1):
		return fail(fmt.Sprintf("end_line %d is before start_line %d", in.EndLine, max(in.StartLine, 1)))
	}
	ranged := in.StartLine > 0 || in.EndLine > 0
	big := info.Size() > maxReadSize
	if big && !ranged {
		return fail(fmt.Sprintf("%s is %d bytes, over the 8 MiB limit; pass start_line and end_line", path, info.Size()))
	}
	if index.Secret(path) {
		return fail(path + " looks like a secret file (keys, tokens, credentials); its content is not returned")
	}
	start, end := max(in.StartLine, 1), in.EndLine
	if end <= 0 {
		end = start + defaultLines - 1
	}
	var src []byte
	var lines []string // file lines first..first+len-1; the newline ending the last line starts no new one
	first, total := 1, 0
	if big {
		// Only the requested lines are kept: the file is streamed, not loaded.
		lines, total, err = lineRange(path, start, end)
		first = start
	} else if src, err = os.ReadFile(path); err == nil && len(src) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
		total = len(lines)
	}
	if err != nil {
		return fail(fmt.Sprintf("cannot read %s: %v", path, err))
	}
	data := map[string]any{"file_path": path, "total_lines": total}
	if ranged {
		end = min(end, total)
		if start > end && (total > 0 || start > 1) { // an empty file reads as nothing from line 1
			return fail(fmt.Sprintf("start_line %d is past the end of the file (%d lines)", start, total))
		}
		data["start_line"], data["end_line"] = start, end
		data["lines"] = numbered(lines, first, start, end)
		return reply("ok", "", data, w)
	}
	// A file the index skips (minified, over the size cap, binary) is not parsed
	// either: parsing a 5 MB bundle took 42 s and 1.3 GB.
	if why := index.Skip(path, info, src[:min(len(src), 8<<10)]); why != "" && why != "no analyzer" {
		if in.Symbol != "" {
			return fail(fmt.Sprintf("%s is not parsed (%s); pass start_line and end_line", path, why))
		}
		end = min(defaultLines, len(lines))
		data["start_line"], data["end_line"] = 1, end
		data["lines"] = numbered(lines, 1, 1, end)
		return reply("ok", "no outline: the file is not parsed ("+why+")", data, w)
	}
	syms, err := parser.Analyze(parser.File{Path: path, Root: w.Root, Source: src})
	if err != nil {
		return fail(fmt.Sprintf("cannot parse %s: %v", path, err))
	}
	if len(syms) > 0 {
		data["language"] = syms[0].Language
	}
	if in.Symbol != "" {
		var found []parser.Symbol
		for _, s := range syms {
			if s.Name == in.Symbol || parser.MatchQualified(s.QualifiedName, in.Symbol) {
				found = append(found, s)
			}
		}
		if len(found) == 0 {
			return fail(fmt.Sprintf("no symbol %q in %s; call without symbol for the outline", in.Symbol, path))
		}
		for i := range found { // a symbol's content can hold a minified line too
			lines := strings.Split(found[i].Content, "\n")
			for j, l := range lines {
				lines[j] = capLine(l)
			}
			found[i].Content = strings.Join(lines, "\n")
			found[i].Signature = clip(found[i].Signature, maxSigBytes)
		}
		data["symbols"] = found
		return reply("ok", fmt.Sprintf("%d symbols named %q", len(found), in.Symbol), data, w)
	}
	outline := make([]Outline, len(syms))
	for i, s := range syms {
		outline[i] = Outline{Name: s.Name, QualifiedName: s.QualifiedName, Kind: string(s.Kind), StartLine: s.StartLine,
			EndLine: s.EndLine, Signature: clip(s.Signature, maxSigBytes), Exported: s.Exported}
	}
	end = min(defaultLines, len(lines))
	data["outline"] = outline
	data["start_line"], data["end_line"] = 1, end
	data["lines"] = numbered(lines, 1, 1, end)
	return reply("ok", "", data, w)
}

// numbered formats lines start..end (1-based, inclusive) with line numbers,
// each cut at maxLineBytes; lines[0] is line first.
func numbered(lines []string, first, start, end int) string {
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%5d | %s\n", i, capLine(lines[i-first]))
	}
	return b.String()
}

// lineRange streams path and returns its lines start..end (end clamped) and
// its line count.
// shortcut: one line is read whole before capLine cuts it; a multi-GB line
// would be held in memory. Upgrade to a capped reader if that shows up.
func lineRange(path string, start, end int) ([]string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var got []string
	n := 0
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			n++
			if n >= start && n <= end {
				got = append(got, strings.TrimSuffix(line, "\n"))
			}
		}
		if err == io.EOF {
			return got, n, nil
		}
		if err != nil {
			return nil, 0, err
		}
	}
}

// capLine cuts l at maxLineBytes and says how much was left out.
func capLine(l string) string {
	if c := clip(l, maxLineBytes); len(c) < len(l) {
		return fmt.Sprintf("%s … (+%d more bytes)", c, len(l)-len(c))
	}
	return l
}

// clip cuts s to at most n bytes without splitting a UTF-8 character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
