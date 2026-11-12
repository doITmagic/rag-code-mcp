// Package parser defines the symbol contract every language analyzer
// produces. Tools never re-derive identity: a symbol's QualifiedName and a
// relation's TargetQualifiedName are computed here, at parse time, through the
// file's own import table.
package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is the symbol kind. Analyzers must use one of these constants.
type Kind string

const (
	KindFunction  Kind = "function"
	KindMethod    Kind = "method"
	KindClass     Kind = "class" // class, struct, trait, enum
	KindInterface Kind = "interface"
	KindType      Kind = "type" // type alias, typedef
	KindVariable  Kind = "variable"
	KindConstant  Kind = "constant"
	KindRoute     Kind = "route"    // Laravel/Express style route registration
	KindHook      Kind = "hook"     // WordPress add_action / add_filter
	KindTemplate  Kind = "template" // Blade, Vue <template>, Go template, HTML section
	KindScene     Kind = "scene"    // Godot scene node
	KindChunk     Kind = "chunk"    // documentation chunk (Markdown section, JSON/YAML/TOML key)
)

// RelationKind is the kind of an outgoing relation from a symbol.
type RelationKind string

const (
	RelCall       RelationKind = "call"
	RelImplements RelationKind = "implements"
	RelExtends    RelationKind = "extends"
	RelUsesType   RelationKind = "uses_type"
	RelImports    RelationKind = "imports"
)

// Resolution says how a relation's TargetID was found. Set by the indexer.
const (
	ResolutionExact     = "exact"     // TargetQualifiedName matched one symbol, exactly or (python, gdscript) as a qualified suffix
	ResolutionUnique    = "unique"    // matched by name only (TargetName), and exactly one symbol has it
	ResolutionAmbiguous = "ambiguous" // several symbols share the name, none matched by qualified name
)

// Symbol is one indexable unit of code or documentation.
//
// QualifiedName is mandatory and canonical: Qualify(Module, Owner, Name).
// Module is the language's own container of the file: Go import path,
// Python dotted module, PHP namespace, JS/TS module path relative to the
// workspace root without extension, GDScript resource path. Owner is the
// enclosing class/struct/interface short name, empty for free symbols.
type Symbol struct {
	ID            string            `json:"id"`
	QualifiedName string            `json:"qualified_name"`
	Name          string            `json:"name"`
	Kind          Kind              `json:"kind"`
	Module        string            `json:"module"`
	Owner         string            `json:"owner,omitempty"`
	Signature     string            `json:"signature,omitempty"`
	Doc           string            `json:"doc,omitempty"`
	Content       string            `json:"content"`
	FilePath      string            `json:"file_path"` // absolute, as given in File.Path
	StartLine     int               `json:"start_line"`
	EndLine       int               `json:"end_line"`
	Language      string            `json:"language"`
	Exported      bool              `json:"exported"`
	Relations     []Relation        `json:"relations,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"` // flat, small, optional (e.g. "framework": "laravel")
}

// Relation is an outgoing edge from a symbol to something it calls, extends,
// implements or uses. TargetQualifiedName is resolved by the analyzer through
// the file's import table; it is empty when the analyzer cannot tell. TargetID
// and Resolution are filled by the indexer's workspace-wide pass.
type Relation struct {
	Kind                RelationKind `json:"kind"`
	TargetName          string       `json:"target_name"`
	TargetQualifiedName string       `json:"target_qualified_name,omitempty"`
	TargetID            string       `json:"target_id,omitempty"`
	Resolution          string       `json:"resolution,omitempty"`
	Line                int          `json:"line"`
	// Value marks a candidate usage, not a call: a function or method named
	// as a value (a handler passed, assigned, returned). The indexer keeps it
	// only when it links exactly to a function or method.
	Value bool `json:"value,omitempty"`
}

// File is the input of an analyzer. Root is the workspace root (absolute);
// analyzers use it to derive Module (e.g. find go.mod, make paths relative).
type File struct {
	Path     string
	Root     string
	Source   []byte
	Language string
}

// Analyzer turns one file into symbols. Implementations must be stateless and
// safe for concurrent use; any caching must be keyed and concurrency-safe.
type Analyzer interface {
	Language() string
	Extensions() []string // lower-case, with leading dot, e.g. ".py"
	Analyze(f File) ([]Symbol, error)
}

// Qualify builds the canonical qualified name. Empty parts are skipped.
func Qualify(module, owner, name string) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{module, owner, name} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}

// Normalize maps every language's separators to "." so that queries written
// as App\Models\User::find, pkg/foo.Bar or Class.method compare equal.
// A segment starting with "/" is a route path (pkg.Load./users, a query
// /users): it is kept as written to the end, so "/" never normalizes to
// nothing and /users never to another symbol's name users. After Normalize,
// a "/" can only be the start of such a path.
func Normalize(s string) string {
	if i := routePath(s); i >= 0 {
		if head := Normalize(s[:i]); head != "" {
			return head + "." + s[i:]
		}
		return s[i:]
	}
	s = strings.ReplaceAll(s, "::", ".")
	s = strings.ReplaceAll(s, "\\", ".")
	s = strings.ReplaceAll(s, "/", ".")
	s = strings.ReplaceAll(s, "->", ".")
	if strings.HasPrefix(s, "(*") { // Go method expression (*T).M
		s = strings.Replace(strings.TrimPrefix(s, "(*"), ").", ".", 1)
	}
	s = strings.Trim(s, ".")
	return s
}

// routePath is the index of the "/" starting a route path segment in s: at
// the start or right after a "." that ends a segment ("./x" and "../x" are
// relative paths, not routes), or -1.
func routePath(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' && (i == 0 || i >= 2 && s[i-1] == '.' && s[i-2] != '.' && s[i-2] != '/') {
			return i
		}
	}
	return -1
}

// MatchQualified reports whether query names qualifiedName: equal after
// normalization, or a suffix of it on a segment boundary. "User.find" matches
// "App.Models.User.find"; "find" matches too; "ser.find" does not.
func MatchQualified(qualifiedName, query string) bool {
	q := Normalize(query)
	n := Normalize(qualifiedName)
	if q == "" || n == "" {
		return false
	}
	if n == q {
		return true
	}
	// the cut must fall before a route path: /static/app.js does not end with the name js
	cut := len(n) - len(q) - 1
	return strings.HasSuffix(n, "."+q) && (strings.IndexByte(n, '/') < 0 || cut < strings.IndexByte(n, '/'))
}

// SymbolID is the stable identity of a symbol: hash of file, qualified name
// and start line. Two symbols with the same qualified name in the same file
// (overloads, redefinitions) stay distinct through the line.
func SymbolID(filePath, qualifiedName string, startLine int) string {
	h := sha256.Sum256([]byte(filepath.ToSlash(filePath) + "\x00" + qualifiedName + "\x00" + strconv.Itoa(startLine)))
	return hex.EncodeToString(h[:16])
}

// Finalize fills ID and QualifiedName (when empty) and clamps lines. Every
// analyzer calls it on its output so the contract holds for all languages.
func Finalize(f File, syms []Symbol) []Symbol {
	seen := make(map[string]bool, len(syms))
	next := map[string]int{} // first id -> the next #n to try
	for i := range syms {
		s := &syms[i]
		if s.FilePath == "" {
			s.FilePath = f.Path
		}
		if s.Language == "" {
			s.Language = f.Language
		}
		if s.QualifiedName == "" {
			s.QualifiedName = Qualify(s.Module, s.Owner, s.Name)
		}
		if s.StartLine < 1 {
			s.StartLine = 1
		}
		if s.EndLine < s.StartLine {
			s.EndLine = s.StartLine
		}
		s.ID = SymbolID(s.FilePath, s.QualifiedName, s.StartLine)
		// Same name on the same line (minified code, CR-only line endings):
		// the n-th one hashes "name#n", so no symbol overwrites another.
		// next remembers where the last search for this name and line stopped:
		// starting at 2 each time is quadratic (minutes for 20,000 on a line).
		for base, n := s.ID, max(next[s.ID], 2); seen[s.ID]; n++ {
			s.ID = SymbolID(s.FilePath, s.QualifiedName+"#"+strconv.Itoa(n), s.StartLine)
			next[base] = n + 1
		}
		seen[s.ID] = true
	}
	return syms
}

// ---- registry ----

var (
	regMu     sync.RWMutex
	analyzers = map[string]Analyzer{} // by extension
	byLang    = map[string]Analyzer{}
)

// Register makes an analyzer available by language and by extension. Later
// registrations for the same extension win, which lets a framework-aware
// analyzer replace a plain one.
func Register(a Analyzer) {
	regMu.Lock()
	defer regMu.Unlock()
	byLang[a.Language()] = a
	for _, ext := range a.Extensions() {
		analyzers[strings.ToLower(ext)] = a
	}
}

// ForFile returns the analyzer for a path by its longest matching extension
// (".blade.php" before ".php"), or nil.
func ForFile(path string) Analyzer {
	base := strings.ToLower(filepath.Base(path))
	regMu.RLock()
	defer regMu.RUnlock()
	for i := 0; i < len(base); i++ {
		if base[i] == '.' {
			if a, ok := analyzers[base[i:]]; ok {
				return a
			}
		}
	}
	return nil
}

// ForLanguage returns the analyzer registered for a language name, or nil.
func ForLanguage(lang string) Analyzer {
	regMu.RLock()
	defer regMu.RUnlock()
	return byLang[lang]
}

// Languages lists registered language names, sorted.
func Languages() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(byLang))
	for l := range byLang {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Analyze picks the analyzer for f.Path, sets f.Language and runs it. Files
// with no analyzer return (nil, nil).
func Analyze(f File) ([]Symbol, error) {
	a := ForFile(f.Path)
	if a == nil {
		return nil, nil
	}
	f.Language = a.Language()
	return a.Analyze(f)
}

// RelModule returns the slash-separated path of p relative to root without
// extension and true. When root is empty or p lies outside it, it returns the
// file stem and false.
func RelModule(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if root == "" || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		b := filepath.Base(p)
		return strings.TrimSuffix(b, filepath.Ext(b)), false
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel)), true
}

// Racy reports whether a file whose modification time is mtime, read at
// readAt, may have changed since without its mtime changing: mtime is not
// older than readAt by more than the coarsest common timestamp tick (FAT32
// keeps 2 s, ecryptfs whole seconds for set times). What is cached by mtime
// (and size) from such a read is checked again by content, or not cached.
func Racy(mtime, readAt time.Time) bool { return !mtime.Before(readAt.Add(-2 * time.Second)) }

// InRoot reports whether p is a regular file whose real path (every symlink
// resolved) lies inside root, resolved too. An analyzer calls it before
// reading a project file other than the one it parses (go.mod, a tsconfig, a
// re-exporting module, an __init__.py), so a ".." or a symlink cannot pull a
// file from outside the workspace into the index; false means "absent".
func InRoot(root, p string) bool {
	rp, err := filepath.EvalSymlinks(p) // first: an absent file costs one failed lookup
	if root == "" || err != nil {
		return false
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	if _, ok := RelModule(rr, rp); !ok {
		return false
	}
	fi, err := os.Stat(rp)
	return err == nil && fi.Mode().IsRegular()
}
