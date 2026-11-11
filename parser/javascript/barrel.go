package javascript

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// barrel is what a workspace module exports, read once from disk to follow
// re-exports: own names it declares ("default" included), named re-exports
// and local aliases (exported name -> "module.name"), and export * sources.
type barrel struct {
	own   map[string]bool
	named map[string]string
	stars []string
	bare  map[string]bool // modules named by a package specifier: not followed
}

// follow resolves the name an importer takes from module m to the module that
// declares it, through m's re-exports: hono's helper/ssg/index.ts doing
// `export * from './ssg'` turns "helper/ssg/index.toSSG" into
// "helper/ssg/ssg.toSSG". Own names win, then named re-exports, then the
// first export * source whose chain reaches a declaration. When nothing
// declares it the result is "m.name", as before.
func (c *fileCtx) follow(m, name string) string {
	if c.noFollow || c.root == "" {
		return m + "." + name
	}
	if qn := c.declarer(m, name, map[string]bool{}); qn != "" {
		return qn
	}
	return m + "." + name
}

// declarer is the qualified name of name in the module that declares it, ""
// when no chain from m reaches one. seen stops cycles and caps the work.
func (c *fileCtx) declarer(m, name string, seen map[string]bool) string {
	key := m + "\x00" + name
	if seen[key] || len(seen) > 64 {
		return ""
	}
	seen[key] = true
	b := c.barrel(m)
	switch {
	case b == nil:
		return ""
	case b.own[name]:
		return m + "." + name
	case b.named[name] != "":
		t := b.named[name]
		i := strings.LastIndex(t, ".")
		if b.bare[t[:i]] {
			return t
		}
		return or(c.declarer(t[:i], t[i+1:], seen), t) // a re-export names its source even when that cannot be read
	}
	for _, s := range b.stars {
		if qn := c.declarer(s, name, seen); qn != "" {
			return qn
		}
	}
	return ""
}

// barrelCache maps root+path of a module file to its barrel and the
// mtime+size it was parsed from, shared by every importer in the process.
var barrelCache sync.Map

type barrelEntry struct {
	mtime time.Time
	size  int64
	b     *barrel
}

// barrel reads the workspace file of module m (a regular file within the size
// cap; .vue and bare packages are not read) and returns its exports, nil when
// there is none.
func (c *fileCtx) barrel(m string) *barrel {
	for _, ext := range []string{".ts", ".tsx", ".js", ".mjs", ".cjs", ".jsx"} {
		p := filepath.Join(c.root, filepath.FromSlash(m)+ext)
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			continue
		}
		key := c.root + "\x00" + p
		if v, ok := barrelCache.Load(key); ok {
			if e := v.(barrelEntry); e.size == info.Size() && e.mtime.Equal(info.ModTime()) {
				return e.b
			}
		}
		b := readBarrel(c.root, m, p, ext)
		barrelCache.Store(key, barrelEntry{info.ModTime(), info.Size(), b})
		return b
	}
	return nil
}

// readBarrel parses the file at p (module m) with pass 1 only, imports not
// followed, and keeps its exports. The result is read-only once stored.
func readBarrel(root, m, p, ext string) *barrel {
	b := &barrel{own: map[string]bool{}, named: map[string]string{}}
	src, err := os.ReadFile(p)
	if err != nil || unbalanced(src) {
		return b
	}
	grammar := map[string]string{".ts": "typescript", ".tsx": "tsx"}[ext]
	tree, lang, err := parser.ParseTree(or(grammar, "javascript"), src)
	if err != nil {
		return b
	}
	defer tree.Release()
	c := newCtx(src, lang, root, m, path.Dir(m), "")
	c.noFollow = true
	for _, nc := range parser.NamedChildren(tree.RootNode()) {
		c.scan(nc)
	}
	for name := range c.defs {
		b.own[c.qual(name)] = true
	}
	b.own["default"] = c.anonDef || c.defs[c.def] != ""
	for name, t := range c.reexp {
		b.named[name] = t
	}
	for name, local := range c.exportAs {
		switch {
		case c.imports[local] != "": // export { x } of an imported x
			b.named[name] = c.imports[local]
		case name != local && c.defs[local] != "": // export { Hono as HonoBase }
			b.named[name] = m + "." + c.qual(local)
		}
	}
	for k, local := range c.props { // export default { getToken }: "default.getToken" -> "m.getToken"
		obj, key, _ := strings.Cut(k, ".")
		t := ""
		switch {
		case local == "": // a method of the inline export default {...}, owned by the component
			t = m + "." + pascal(path.Base(m)) + "." + key
		case c.imports[local] != "":
			t = c.imports[local]
		case c.defs[local] != "":
			t = m + "." + c.qual(local)
		default:
			continue
		}
		b.named[obj+"."+key] = t // import { api } of `export const api = {...}; export default api`
		b.named[c.qual(obj)+"."+key] = t
	}
	b.stars, b.bare = c.stars, c.bare
	return b
}
