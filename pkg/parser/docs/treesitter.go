package docs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

type TreeSitterParser struct{}

func NewTreeSitterParser() *TreeSitterParser {
	return &TreeSitterParser{}
}

func (p *TreeSitterParser) Parse(source []byte, filePath string, ext string) ([]parser.Symbol, error) {
	langInfo := grammars.DetectLanguage(filePath)
	if langInfo == nil {
		return nil, fmt.Errorf("no native treesitter support mapped for %s", ext)
	}

	langObj := langInfo.Language()
	parserTs := gotreesitter.NewParser(langObj)
	tree, err := parserTs.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("ts parse error: %w", err)
	}

	langName := langInfo.Name
	var symbols []parser.Symbol
	root := tree.RootNode()

	var walk func(node *gotreesitter.Node, parentSig string)
	walk = func(node *gotreesitter.Node, parentSig string) {
		nodeLen := int(node.EndByte() - node.StartByte())

		if nodeLen < 5 {
			// Skip too small block naturally, but we must recurse its children just in case they hold valid stuff
			for i := 0; i < node.ChildCount(); i++ {
				walk(node.Child(i), parentSig)
			}
			return
		}

		// A leaf or a reasonably sized chunk (~1500 chars) -> make it a valid symbol chunk
		if node.ChildCount() == 0 || nodeLen <= 1500 {
			text := strings.TrimSpace(node.Text(source))
			if len(text) < 10 {
				for i := 0; i < node.ChildCount(); i++ {
					walk(node.Child(i), parentSig)
				}
				return
			}

			// Prevent massive leaf nodes (e.g. 50MB SQL INSERT values) from crashing Ollama
			if len(text) > 8192 {
				text = text[:8192] + "\n...[TRUNCATED]"
			}

			startLine := int(node.StartPoint().Row) + 1
			endLine := int(node.EndPoint().Row) + 1

			sig := parentSig
			if sig == "" {
				sig = node.Type(langObj)
			} else {
				sig = parentSig + " > " + node.Type(langObj)
			}

			symbols = append(symbols, parser.Symbol{
				Name:      filepath.Base(filePath),
				Type:      "document_chunk",
				Package:   "",
				Content:   text,
				Signature: sig,
				Docstring: "",
				StartLine: startLine,
				EndLine:   endLine,
				FilePath:  filePath,
				Language:  langName,
				IsPublic:  true,
				Metadata: map[string]interface{}{
					"chunk_type": "documentation",
				},
			})
			return
		}

		// If node is large, compute a new signature step and recurse
		newSig := parentSig
		if node.IsNamed() {
			if newSig == "" {
				newSig = node.Type(langObj)
			} else {
				// Prevent signature from becoming giant (max 3 levels breadcrumb)
				parts := strings.Split(newSig, " > ")
				if len(parts) < 3 {
					newSig = newSig + " > " + node.Type(langObj)
				}
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i), newSig)
		}
	}

	for i := 0; i < root.ChildCount(); i++ {
		walk(root.Child(i), "")
	}

	return symbols, nil
}
