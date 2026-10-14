package docs

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const (
	maxTargetChunkBytes = 1500
	maxTruncateBytes    = 8192
	smallFileThreshold  = 4000
)

// TreeSitterParser parses documentation files using tree-sitter.
// Caches Parser instances per language to avoid re-allocating expensive lookup tables.
type TreeSitterParser struct {
	mu      sync.Mutex
	parsers map[string]*gotreesitter.Parser
}

func NewTreeSitterParser() *TreeSitterParser {
	return &TreeSitterParser{
		parsers: make(map[string]*gotreesitter.Parser),
	}
}

func (p *TreeSitterParser) getOrCreateParser(lang *grammars.LangEntry) *gotreesitter.Parser {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cached, ok := p.parsers[lang.Name]; ok {
		return cached
	}
	tsParser := gotreesitter.NewParser(lang.Language())
	p.parsers[lang.Name] = tsParser
	return tsParser
}

// ReleaseResources drops cached tree-sitter parsers so the GC can reclaim arena memory.
func (p *TreeSitterParser) ReleaseResources() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parsers = make(map[string]*gotreesitter.Parser)
}

func appendSignature(parentSig, nodeType string) string {
	if nodeType == "" || strings.HasPrefix(nodeType, "_") {
		return parentSig
	}
	if parentSig == "" {
		return nodeType
	}
	parts := strings.Split(parentSig, " > ")
	if len(parts) >= 3 {
		return parentSig
	}
	if parts[len(parts)-1] == nodeType {
		return parentSig
	}
	return parentSig + " > " + nodeType
}

func sliceUTF8Safe(source []byte, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(source) {
		end = len(source)
	}
	if start >= end {
		return ""
	}
	if end-start > maxTruncateBytes {
		limit := start + maxTruncateBytes
		if limit > len(source) {
			limit = len(source)
		}
		for limit > start && !utf8.RuneStart(source[limit]) {
			limit--
		}
		return strings.TrimSpace(string(source[start:limit])) + "\n...[TRUNCATED]"
	}
	return strings.TrimSpace(string(source[start:end]))
}

func (p *TreeSitterParser) fallbackLineChunks(source []byte, filePath, langName string) []parser.Symbol {
	trimmed := bytes.TrimSpace(source)
	if len(trimmed) == 0 {
		return nil
	}

	rawLines := strings.Split(strings.ReplaceAll(string(source), "\r\n", "\n"), "\n")
	if len(source) <= smallFileThreshold {
		text := sliceUTF8Safe(source, 0, len(source))
		if text == "" {
			return nil
		}
		return []parser.Symbol{
			{
				Name:      filepath.Base(filePath),
				Type:      "document_chunk",
				Content:   text,
				Signature: langName,
				StartLine: 1,
				EndLine:   len(rawLines),
				FilePath:  filePath,
				Language:  langName,
				IsPublic:  true,
				Metadata: map[string]interface{}{
					"chunk_type": "documentation",
				},
			},
		}
	}

	var symbols []parser.Symbol
	var chunkLines []string
	chunkBytes := 0
	startLine := 1

	flush := func(endLine int) {
		if len(chunkLines) == 0 {
			return
		}
		raw := []byte(strings.Join(chunkLines, "\n"))
		text := sliceUTF8Safe(raw, 0, len(raw))
		chunkLines = nil
		chunkBytes = 0
		if text == "" {
			return
		}
		symbols = append(symbols, parser.Symbol{
			Name:      filepath.Base(filePath),
			Type:      "document_chunk",
			Content:   text,
			Signature: langName,
			StartLine: startLine,
			EndLine:   endLine,
			FilePath:  filePath,
			Language:  langName,
			IsPublic:  true,
			Metadata: map[string]interface{}{
				"chunk_type": "documentation",
			},
		})
	}

	for idx, line := range rawLines {
		lineNum := idx + 1
		isTopLevelBoundary := len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#'
		if (isTopLevelBoundary && chunkBytes >= maxTargetChunkBytes) || chunkBytes+len(line)+1 > smallFileThreshold {
			flush(lineNum - 1)
			startLine = lineNum
		}
		if len(chunkLines) == 0 {
			startLine = lineNum
		}
		chunkLines = append(chunkLines, line)
		chunkBytes += len(line) + 1
	}
	flush(len(rawLines))

	return symbols
}

func (p *TreeSitterParser) Parse(source []byte, filePath string, ext string) ([]parser.Symbol, error) {
	langInfo := grammars.DetectLanguage(filePath)
	if langInfo == nil {
		return nil, fmt.Errorf("no native treesitter support mapped for %s", ext)
	}

	langName := langInfo.Name
	// Tree-sitter YAML grammar frequently stops early or produces _stream error nodes;
	// use clean line/block chunking for YAML and as a fallback when tree-sitter fails.
	if langName == "yaml" || ext == ".yaml" || ext == ".yml" {
		return p.fallbackLineChunks(source, filePath, langName), nil
	}

	langObj := langInfo.Language()
	parserTs := p.getOrCreateParser(langInfo)
	tree, err := parserTs.Parse(source)
	if err != nil {
		return p.fallbackLineChunks(source, filePath, langName), nil
	}
	defer tree.Release()

	root := tree.RootNode()
	trimmedLen := len(bytes.TrimSpace(source))
	if root == nil || root.HasError() || int(root.EndByte()) < trimmedLen {
		return p.fallbackLineChunks(source, filePath, langName), nil
	}

	var symbols []parser.Symbol

	emitSpan := func(startNode, endNode *gotreesitter.Node, sig string) {
		start := int(startNode.StartByte())
		end := int(endNode.EndByte())
		text := sliceUTF8Safe(source, start, end)
		if text == "" {
			return
		}
		if sig == "" {
			t := startNode.Type(langObj)
			if t != "" && !strings.HasPrefix(t, "_") {
				sig = t
			} else {
				sig = langName
			}
		}
		symbols = append(symbols, parser.Symbol{
			Name:      filepath.Base(filePath),
			Type:      "document_chunk",
			Package:   "",
			Content:   text,
			Signature: sig,
			Docstring: "",
			StartLine: int(startNode.StartPoint().Row) + 1,
			EndLine:   int(endNode.EndPoint().Row) + 1,
			FilePath:  filePath,
			Language:  langName,
			IsPublic:  true,
			Metadata: map[string]interface{}{
				"chunk_type": "documentation",
			},
		})
	}

	var walk func(node *gotreesitter.Node, parentSig string)
	walk = func(node *gotreesitter.Node, parentSig string) {
		nodeLen := int(node.EndByte() - node.StartByte())
		if nodeLen <= 0 {
			return
		}

		nodeSig := parentSig
		if node.IsNamed() {
			nodeSig = appendSignature(parentSig, node.Type(langObj))
		}

		// Collect named children
		var namedChildren []*gotreesitter.Node
		for i := 0; i < node.ChildCount(); i++ {
			ch := node.Child(i)
			if ch != nil && ch.IsNamed() && int(ch.EndByte()-ch.StartByte()) > 0 {
				namedChildren = append(namedChildren, ch)
			}
		}

		// Leaf or reasonably-sized node -> emit directly as a single chunk
		if len(namedChildren) == 0 || nodeLen <= maxTargetChunkBytes {
			emitSpan(node, node, nodeSig)
			return
		}

		// If the node is a single-child wrapper (e.g. document -> object), unwrap directly
		if len(namedChildren) == 1 {
			walk(namedChildren[0], nodeSig)
			return
		}

		// Coalesce consecutive small named sibling nodes into ~1500 byte chunks
		var spanStart, spanEnd *gotreesitter.Node
		var spanSig string

		flushSpan := func() {
			if spanStart != nil && spanEnd != nil {
				emitSpan(spanStart, spanEnd, spanSig)
				spanStart = nil
				spanEnd = nil
				spanSig = ""
			}
		}

		for _, ch := range namedChildren {
			chLen := int(ch.EndByte() - ch.StartByte())
			chSig := appendSignature(nodeSig, ch.Type(langObj))

			if chLen > maxTargetChunkBytes {
				flushSpan()
				walk(ch, nodeSig)
				continue
			}

			if spanStart == nil {
				spanStart = ch
				spanEnd = ch
				spanSig = chSig
			} else if int(ch.EndByte()-spanStart.StartByte()) <= maxTargetChunkBytes {
				spanEnd = ch
			} else {
				flushSpan()
				spanStart = ch
				spanEnd = ch
				spanSig = chSig
			}
		}
		flushSpan()
	}

	walk(root, "")

	if len(symbols) == 0 && trimmedLen > 0 {
		return p.fallbackLineChunks(source, filePath, langName), nil
	}

	return symbols, nil
}
