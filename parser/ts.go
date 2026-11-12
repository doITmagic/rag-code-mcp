package parser

import (
	"fmt"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Tree-sitter helpers shared by every analyzer. Parsers are pooled per
// grammar, so analyzers stay stateless and concurrent calls never share a
// *gotreesitter.Parser.

// parseTimeoutMicros bounds one parse. Error recovery in gotreesitter is
// quadratic on cascades of unclosed brackets, so garbage that passes the
// indexer's binary check could otherwise run for minutes.
const parseTimeoutMicros = 3 * 1000 * 1000

var (
	poolMu sync.Mutex
	pools  = map[string]*gotreesitter.ParserPool{}
)

// grammar returns the registered grammar entry for a name such as "python",
// "php", "blade", "vue", "gdscript", "markdown".
func grammar(name string) (*grammars.LangEntry, error) {
	e := grammars.DetectLanguageByName(name)
	if e == nil {
		return nil, fmt.Errorf("parser: no tree-sitter grammar named %q", name)
	}
	return e, nil
}

func pool(name string) (*gotreesitter.ParserPool, *gotreesitter.Language, error) {
	e, err := grammar(name)
	if err != nil {
		return nil, nil, err
	}
	poolMu.Lock()
	defer poolMu.Unlock()
	p, ok := pools[name]
	if !ok {
		p = gotreesitter.NewParserPool(e.Language(), gotreesitter.WithParserPoolTimeoutMicros(parseTimeoutMicros))
		pools[name] = p
	}
	return p, p.Language(), nil
}

// ParseTree parses src with the named grammar. The returned Language is
// needed for Node.Type and Node.ChildByFieldName.
func ParseTree(grammar string, src []byte) (*gotreesitter.Tree, *gotreesitter.Language, error) {
	p, lang, err := pool(grammar)
	if err != nil {
		return nil, nil, err
	}
	t, err := p.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("parser: %s: %w", grammar, err)
	}
	// A parse cut short holds part of the file at best: an error, so the file
	// counts as failed instead of being indexed as empty.
	if r := t.ParseStopReason(); r == gotreesitter.ParseStopTimeout || r == gotreesitter.ParseStopMemoryBudget {
		return nil, nil, fmt.Errorf("parser: %s: parse stopped: %s", grammar, r)
	}
	return t, lang, nil
}

// Walk visits every named node depth-first. Return false from fn to skip the
// node's children.
func Walk(n *gotreesitter.Node, fn func(n *gotreesitter.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	for _, ch := range NamedChildren(n) {
		Walk(ch, fn)
	}
}

// NamedChildren returns the named children of n in order. Use it instead of
// NamedChildCount/NamedChild loops: gotreesitter rescans every child on each
// of those calls, which makes such loops quadratic in the number of siblings.
func NamedChildren(n *gotreesitter.Node) []*gotreesitter.Node {
	if n == nil {
		return nil
	}
	all := n.Children()
	out := make([]*gotreesitter.Node, 0, len(all))
	for _, ch := range all {
		if ch.IsNamed() {
			out = append(out, ch)
		}
	}
	return out
}

// Field returns the child for a field name, or nil.
func Field(n *gotreesitter.Node, lang *gotreesitter.Language, field string) *gotreesitter.Node {
	if n == nil {
		return nil
	}
	return n.ChildByFieldName(field, lang)
}

// Text returns the source text of a node, "" for nil.
func Text(n *gotreesitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	return n.Text(src)
}

// Lines returns 1-based start and end lines of a node.
func Lines(n *gotreesitter.Node) (int, int) {
	if n == nil {
		return 0, 0
	}
	return int(n.StartPoint().Row) + 1, int(n.EndPoint().Row) + 1
}

// ReleaseParsers drops the pooled parsers and tree-sitter arenas. Call it
// after a full index run to hand memory back to the OS.
func ReleaseParsers() {
	poolMu.Lock()
	pools = map[string]*gotreesitter.ParserPool{}
	poolMu.Unlock()
	gotreesitter.DrainArenaPools()
}
