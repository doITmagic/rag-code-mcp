package gdscript

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

type extResource struct {
	Type string
	Path string
}

type sceneNode struct {
	Path     string
	Type     string
	Script   string
	Instance string
	Groups   []string
}

type sceneConnection struct {
	Signal string
	From   string
	To     string
	Method string
}

// parseScene summarises a text scene (.tscn) as one symbol: its node tree,
// the scripts attached to nodes, instanced sub-scenes and signal connections.
// The raw file is mostly serialized property data, so the symbol content is
// a readable outline rather than the source. Relations point at the attached
// scripts and instanced scenes (by file name) and at the handler methods of
// signal connections, linking scenes to their scripts.
func (a *Analyzer) parseScene(source []byte, path string) ([]pkgParser.Symbol, error) {
	var symbols []pkgParser.Symbol
	err := a.parse(source, path, func(root *gotreesitter.Node, lang *gotreesitter.Language) {
		symbols = sceneSymbols(root, lang, source, path)
	})
	return symbols, err
}

func sceneSymbols(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, path string) []pkgParser.Symbol {
	var (
		header      string
		resources   = map[string]extResource{}
		nodes       []sceneNode
		connections []sceneConnection
	)

	for i := 0; i < root.ChildCount(); i++ {
		section := root.Child(i)
		if section.Type(lang) != "section" {
			continue
		}
		kind, attrs, props := readSection(section, lang, src)
		switch kind {
		case "gd_scene":
			header = kind
		case "ext_resource":
			resources[attrs["id"]] = extResource{Type: attrs["type"], Path: attrs["path"]}
		case "node":
			node := sceneNode{
				Path:     nodePath(attrs["name"], attrs["parent"]),
				Type:     attrs["type"],
				Instance: extResourceID(attrs["instance"]),
				Groups:   splitList(attrs["groups"]),
			}
			node.Script = extResourceID(props["script"])
			nodes = append(nodes, node)
		case "connection":
			connections = append(connections, sceneConnection{
				Signal: attrs["signal"],
				From:   attrs["from"],
				To:     attrs["to"],
				Method: attrs["method"],
			})
		}
	}
	if header == "" && len(nodes) == 0 {
		return nil
	}

	name := fileStem(path)
	rels := &relationSet{}
	var scripts, instances []string
	var content strings.Builder
	fmt.Fprintf(&content, "Godot scene %s\n", filepath.Base(path))

	if len(nodes) > 0 {
		content.WriteString("Nodes:\n")
	}
	nodeMeta := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		line := "  " + n.Path
		if n.Type != "" {
			line += " (" + n.Type + ")"
		}
		meta := map[string]any{"path": n.Path, "type": n.Type}
		if res, ok := resources[n.Instance]; ok {
			line += " instance=" + res.Path
			meta["instance"] = res.Path
			instances = append(instances, res.Path)
			rels.add(pkgParser.Relation{TargetName: fileStem(res.Path), Type: pkgParser.RelDependency})
		}
		if res, ok := resources[n.Script]; ok {
			line += " script=" + res.Path
			meta["script"] = res.Path
			scripts = append(scripts, res.Path)
			rels.add(pkgParser.Relation{TargetName: fileStem(res.Path), Type: pkgParser.RelDependency})
		}
		if len(n.Groups) > 0 {
			line += " groups=" + strings.Join(n.Groups, ",")
			meta["groups"] = n.Groups
		}
		content.WriteString(line + "\n")
		nodeMeta = append(nodeMeta, meta)
	}

	if len(connections) > 0 {
		content.WriteString("Signal connections:\n")
	}
	// Connections name the root ".", which reads poorly in the outline.
	display := func(path string) string {
		if path == "." && len(nodes) > 0 {
			return nodes[0].Path
		}
		return path
	}
	connMeta := make([]map[string]any, 0, len(connections))
	for _, c := range connections {
		fmt.Fprintf(&content, "  %s.%s -> %s.%s()\n", display(c.From), c.Signal, display(c.To), c.Method)
		connMeta = append(connMeta, map[string]any{"signal": c.Signal, "from": c.From, "to": c.To, "method": c.Method})
		// No receiver: the handler lives in a script attached to another
		// file, so it can only resolve by name.
		rels.add(pkgParser.Relation{TargetName: c.Method, Type: pkgParser.RelCalls})
	}

	signature := "scene " + filepath.Base(path)
	rootName, rootType := "", ""
	if len(nodes) > 0 {
		rootName, rootType = nodes[0].Path, nodes[0].Type
		signature += " root=" + rootName
		if rootType != "" {
			signature += " (" + rootType + ")"
		}
	}

	return []pkgParser.Symbol{{
		Name:          name,
		QualifiedName: name,
		Type:          "scene",
		Package:       name,
		Content:       content.String(),
		Signature:     signature,
		StartLine:     1,
		EndLine:       int(root.EndPoint().Row) + 1,
		FilePath:      path,
		Language:      "gdscript",
		IsPublic:      true,
		Relations:     rels.list,
		Metadata: map[string]any{
			"gdscript_kind": "scene",
			"root_node":     rootName,
			"root_type":     rootType,
			"nodes":         nodeMeta,
			"scripts":       scripts,
			"instances":     instances,
			"connections":   connMeta,
		},
	}}
}

// readSection returns a section's tag (`node`, `ext_resource`, ...), its
// header attributes and its `key = value` properties. String values are
// unquoted; other values keep their source text (`ExtResource("1_x")`).
func readSection(section *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (string, map[string]string, map[string]string) {
	var kind string
	attrs := map[string]string{}
	props := map[string]string{}
	for i := 0; i < section.ChildCount(); i++ {
		c := section.Child(i)
		switch c.Type(lang) {
		case "identifier":
			if kind == "" {
				kind = c.Text(src)
			}
		case "attribute", "property":
			var key, value string
			for j := 0; j < c.ChildCount(); j++ {
				part := c.Child(j)
				switch t := part.Type(lang); {
				case key == "" && (t == "identifier" || t == "path"):
					key = part.Text(src)
				case t == "=":
				case key != "" && value == "":
					value = part.Text(src)
					if t == "string" {
						value = strings.Trim(value, `"`)
					}
				}
			}
			if c.Type(lang) == "attribute" {
				attrs[key] = value
			} else {
				props[key] = value
			}
		}
	}
	return kind, attrs, props
}

// nodePath returns a node's path relative to the scene root, the way a
// script on the root reaches it (`$Body/Arm`); the root is listed by name.
// In .tscn files the root has no parent, its children have parent=".", and
// deeper nodes name their parent relative to the root ("Body").
func nodePath(name, parent string) string {
	if parent == "" || parent == "." {
		return name
	}
	return parent + "/" + name
}

// extResourceID extracts the id from `ExtResource("1_x")` (Godot 4) or
// `ExtResource( 1 )` (Godot 3).
func extResourceID(value string) string {
	inner, ok := strings.CutPrefix(value, "ExtResource")
	if !ok {
		return ""
	}
	inner = strings.TrimSpace(inner)
	inner = strings.TrimSuffix(strings.TrimPrefix(inner, "("), ")")
	return strings.Trim(strings.TrimSpace(inner), `"`)
}

// splitList parses an array literal of strings: `["enemies", "hittable"]`.
func splitList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.Trim(strings.TrimSpace(part), `"`); part != "" {
			out = append(out, part)
		}
	}
	return out
}
