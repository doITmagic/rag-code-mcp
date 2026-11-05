// Package gdscript analyzes Godot scripts (.gd) with tree-sitter and text
// scenes (.tscn) with a line scanner. Module is the Godot resource path of
// the file without its extension: <root>/actors/player.gd is
// "res://actors/player", and so is the scene next to it.
package gdscript

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

func init() { parser.Register(analyzer{}) }

type analyzer struct{}

func (analyzer) Language() string     { return "gdscript" }
func (analyzer) Extensions() []string { return []string{".gd", ".tscn"} }

func (analyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	if strings.EqualFold(filepath.Ext(f.Path), ".tscn") {
		return parser.Finalize(f, scene(f)), nil
	}
	syms, err := script(f)
	if err != nil {
		return nil, err
	}
	return parser.Finalize(f, syms), nil
}

// module returns "res://" + the path relative to the root without extension.
// A file outside the root, or without one, uses its stem.
func module(f parser.File) string {
	m, _ := parser.RelModule(f.Root, f.Path)
	return "res://" + m
}

// resModule maps a "res://x/y.gd" path to its module "res://x/y". A relative
// path ("../base.gd") is taken from the directory of from and simplified, as
// Godot does. Other schemes (uid://) are unknown and return "".
func resModule(p, from string) string {
	if strings.Contains(p, "://") {
		if !strings.HasPrefix(p, "res://") {
			return ""
		}
	} else {
		p = from[:strings.LastIndex(from, "/")+1] + p
	}
	p = strings.TrimSuffix(strings.TrimPrefix(p, "res://"), filepath.Ext(p))
	return "res://" + path.Clean(p)
}

func stem(p string) string {
	return strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], filepath.Ext(p))
}
