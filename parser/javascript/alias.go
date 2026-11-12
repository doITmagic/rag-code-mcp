package javascript

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// aliases is one file's import-alias table: the "paths" of the nearest
// tsconfig.json / jsconfig.json, as root-relative targets, and the project
// directory (nearest package.json) the bundler conventions are relative to.
type aliases struct {
	paths   []pathAlias // longest prefix first, the order TypeScript tries them
	project string
}

type pathAlias struct {
	prefix, suffix string // the pattern split at its "*"
	wildcard       bool
	targets        []string // root-relative, each with at most one "*"
}

// conventions are the Nuxt / Vite / Vue CLI aliases that work without a
// tsconfig mapping (Nuxt writes its own into .nuxt/, which is not in the
// repo): prefix -> directories under the project, tried in order.
var conventions = []struct {
	prefix string
	dirs   []string
}{
	{"~~/", []string{""}},
	{"@@/", []string{""}},
	{"~/", []string{"app", "src", ""}},
	{"@/", []string{"app", "src", ""}},
	{"#shared/", []string{"shared"}},
}

// aliasSpec resolves a non-relative specifier through tsconfig "paths", then
// the conventions. Only a target whose file exists counts, so a package name
// that merely looks like an alias stays a package: "" when nothing matched.
func (c *fileCtx) aliasSpec(spec string) string {
	if c.root == "" {
		return ""
	}
	if c.alias == nil {
		c.alias = loadAliases(c.root, c.dir)
	}
	for _, a := range c.alias.paths {
		star, ok := a.match(spec)
		if !ok {
			continue
		}
		for _, t := range a.targets {
			if m := c.existing(strings.Replace(t, "*", star, 1)); m != "" {
				return m
			}
		}
		return "" // TypeScript stops at the first matching pattern
	}
	for _, cv := range conventions {
		rest, ok := strings.CutPrefix(spec, cv.prefix)
		if !ok {
			continue
		}
		for _, d := range cv.dirs {
			if m := c.existing(path.Join(c.alias.project, d, rest)); m != "" {
				return m
			}
		}
	}
	return ""
}

func (a pathAlias) match(spec string) (string, bool) {
	if !a.wildcard {
		return "", spec == a.prefix
	}
	if len(spec) < len(a.prefix)+len(a.suffix) || !strings.HasPrefix(spec, a.prefix) || !strings.HasSuffix(spec, a.suffix) {
		return "", false
	}
	return spec[len(a.prefix) : len(spec)-len(a.suffix)], true
}

// existing is the module of a root-relative path whose source file exists.
func (c *fileCtx) existing(p string) string {
	if p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	m := c.modulePath(p)
	if !hasSource(filepath.Join(c.root, filepath.FromSlash(m))) {
		return ""
	}
	return m
}

// loadAliases walks from dir up to the root for the nearest tsconfig.json or
// jsconfig.json and the nearest package.json.
func loadAliases(root, dir string) *aliases {
	a := &aliases{project: "."}
	foundCfg, foundPkg := false, false
	for d := dir; ; d = path.Dir(d) {
		if !foundPkg && fileExists(filepath.Join(root, filepath.FromSlash(d), "package.json")) {
			a.project, foundPkg = d, true
		}
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			if !foundCfg && fileExists(filepath.Join(root, filepath.FromSlash(d), name)) {
				a.paths, foundCfg = readPaths(root, path.Join(d, name), 0), true
			}
		}
		if foundCfg && foundPkg || d == "." || d == "/" || d == "" {
			break
		}
	}
	sort.SliceStable(a.paths, func(i, j int) bool { return len(a.paths[i].prefix) > len(a.paths[j].prefix) })
	return a
}

type tsconfig struct {
	Extends         json.RawMessage `json:"extends"`
	CompilerOptions struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// readPaths reads the "paths" of a root-relative config, following relative
// "extends" until one defines them.
// shortcut: targets are relative to the baseUrl of the config that defines
// "paths" (or its directory), not an inherited baseUrl; package "extends"
// (e.g. "@tsconfig/node20") are not followed. Upgrade if a repo needs either.
func readPaths(root, cfg string, depth int) []pathAlias {
	p := filepath.Join(root, filepath.FromSlash(cfg))
	if !parser.InRoot(root, p) { // also keeps a FIFO, `..` and symlinks out of the workspace from being read
		return nil
	}
	if info, err := os.Stat(p); err != nil || info.Size() > 4<<20 {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil || depth > 4 {
		return nil
	}
	var tc tsconfig
	if json.Unmarshal(stripJSONC(raw), &tc) != nil {
		return nil
	}
	dir := path.Dir(cfg)
	if tc.CompilerOptions.Paths == nil {
		var ext []string
		if json.Unmarshal(tc.Extends, &ext) != nil {
			var one string
			if json.Unmarshal(tc.Extends, &one) == nil && one != "" {
				ext = []string{one}
			}
		}
		for i := len(ext) - 1; i >= 0; i-- { // a later entry overrides an earlier one
			if !strings.HasPrefix(ext[i], ".") {
				continue
			}
			p := path.Join(dir, ext[i])
			if !strings.HasSuffix(p, ".json") && !fileExists(filepath.Join(root, filepath.FromSlash(p))) {
				p += ".json"
			}
			if out := readPaths(root, p, depth+1); out != nil {
				return out
			}
		}
		return nil
	}
	base := dir
	if tc.CompilerOptions.BaseURL != nil {
		base = path.Join(dir, *tc.CompilerOptions.BaseURL)
	}
	out := make([]pathAlias, 0, len(tc.CompilerOptions.Paths))
	for pattern, targets := range tc.CompilerOptions.Paths {
		a := pathAlias{prefix: pattern}
		if i := strings.Index(pattern, "*"); i >= 0 {
			a = pathAlias{prefix: pattern[:i], suffix: pattern[i+1:], wildcard: true}
		}
		for _, t := range targets {
			a.targets = append(a.targets, path.Join(base, t))
		}
		out = append(out, a)
	}
	return out
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// stripJSONC drops the // and /* */ comments and trailing commas that
// tsconfig.json allows and encoding/json does not.
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '"':
			j := i + 1
			for ; j < len(b) && b[j] != '"'; j++ {
				if b[j] == '\\' {
					j++
				}
			}
			j = min(j, len(b)-1)
			out = append(out, b[i:j+1]...)
			i = j
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			for i+1 < len(b) && b[i+1] != '\n' {
				i++
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, b[i])
		}
	}
	// Trailing commas: comments are gone, so only whitespace can sit between.
	res := out[:0:0]
	for i := 0; i < len(out); i++ {
		if out[i] == '"' {
			j := i + 1
			for ; j < len(out) && out[j] != '"'; j++ {
				if out[j] == '\\' {
					j++
				}
			}
			j = min(j, len(out)-1)
			res = append(res, out[i:j+1]...)
			i = j
			continue
		}
		if out[i] == ',' {
			k := i + 1
			for k < len(out) && strings.ContainsRune(" \t\r\n", rune(out[k])) {
				k++
			}
			if k < len(out) && (out[k] == '}' || out[k] == ']') {
				continue
			}
		}
		res = append(res, out[i])
	}
	return res
}
