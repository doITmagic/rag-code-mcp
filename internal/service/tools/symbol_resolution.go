package tools

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// A short name is not an identity. Prefer an explicit identity, then require
// a unique candidate in the supplied file or workspace.
func resolveSymbol(ctx context.Context, eng *engine.Engine, wsID, root, name, file string) (*storage.SearchResult, string) {
	results, err := searchSymbolCandidates(ctx, eng, wsID, root, name, 100)
	if err != nil {
		return nil, "unresolved"
	}
	return uniqueSymbol(results, file)
}

func searchSymbolCandidates(ctx context.Context, eng *engine.Engine, wsID, root, name string, limit int) ([]storage.SearchResult, error) {
	results, err := eng.SearchByName(ctx, wsID, name, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": name}, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	var symbols []storage.SearchResult
	// PHP Dotenv::load may name the class without its namespace.
	if i := strings.LastIndex(name, "::"); i > 0 {
		results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"language": "php", "type": "method", "name": name[i+2:]}, 0)
		for _, r := range results {
			if qualified, _ := r.Point.Payload["qualified_name"].(string); strings.HasSuffix(qualified, "\\"+name) {
				symbols = append(symbols, r)
			}
		}
		return symbols, err
	}
	for _, reading := range splitGoImportPath(name) {
		found, err := searchGoQualified(ctx, eng, wsID, reading.name, reading.importPath != "", limit)
		if err != nil {
			return nil, err
		}
		if reading.importPath != "" {
			found = filterByImportPath(found, reading.importPath, root)
		}
		symbols = append(symbols, found...)
	}
	return symbols, nil
}

// A name under an import path starts with its package, so its two parts are
// a package.Function or package.Type, never a Type.Method.
func searchGoQualified(ctx context.Context, eng *engine.Engine, wsID, name string, underImportPath bool, limit int) ([]storage.SearchResult, error) {
	filters := goMethodFilters(name)
	if filters == nil {
		return nil, nil
	}
	var symbols []storage.SearchResult
	if !underImportPath || filters["package"] != nil {
		var err error
		symbols, err = eng.ExactSearchPolyglot(ctx, wsID, filters, limit)
		// (*T).Method names only a method.
		if err != nil || filters["package"] != nil || strings.ContainsAny(name, "(*") {
			return symbols, err
		}
	}
	// A two-part name may also be a Go package.Function or package.Type,
	// also when a type shares the package's name: keep both readings.
	results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{
		"language": "go", "package": filters["metadata.receiver"], "name": filters["name"],
	}, limit)
	for _, r := range results {
		if r.Point.Payload["type"] != "method" {
			symbols = append(symbols, r)
		}
	}
	return symbols, err
}

type goImportReading struct{ importPath, name string }

// splitGoImportPath splits github.com/spf13/pflag.FlagSet.Parse into the
// package's import path and the name within it, pflag.FlagSet.Parse: the
// index stores only the package's name. A dot may also belong to the path,
// as in gopkg.in/yaml.v3.Marshal, so the name within the package is read as
// both Type.Method and Function, its package named by the segment's first
// dotted part.
func splitGoImportPath(name string) []goImportReading {
	i := strings.LastIndex(name, "/")
	if i < 0 {
		return []goImportReading{{name: name}}
	}
	parts := strings.Split(name[i+1:], ".")
	var readings []goImportReading
	for k := 1; k <= 2 && k < len(parts); k++ {
		readings = append(readings, goImportReading{
			importPath: name[:i+1] + strings.Join(parts[:len(parts)-k], "."),
			name:       parts[0] + "." + strings.Join(parts[len(parts)-k:], "."),
		})
	}
	return readings
}

// goMethodFilters maps [pkg.]Receiver.Method, including (*T).Method, to the
// metadata Go stores method ownership in, since it has no qualified_name.
func goMethodFilters(name string) map[string]interface{} {
	parts := strings.Split(name, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return nil
	}
	parts[len(parts)-2] = strings.Trim(parts[len(parts)-2], "(*)")
	for _, part := range parts {
		if part == "" {
			return nil
		}
	}
	filters := map[string]interface{}{
		"language": "go", "type": "method", "name": parts[len(parts)-1],
		"metadata.receiver": parts[len(parts)-2],
	}
	if len(parts) == 3 {
		filters["package"] = parts[0]
	}
	return filters
}

// qualifierRulesOut reports whether an unresolved name's qualifier was
// checked: it names an indexed Go type, which has no such method. Interfaces
// (methods not indexed by receiver), types with embedded fields (promoted
// methods), external packages and other languages are not modelled here.
func qualifierRulesOut(ctx context.Context, eng *engine.Engine, wsID, root, name string) bool {
	for _, reading := range splitGoImportPath(name) {
		filters := goMethodFilters(reading.name)
		if filters == nil {
			continue
		}
		typeFilters := map[string]interface{}{"language": "go", "type": "type", "name": filters["metadata.receiver"]}
		if pkg, ok := filters["package"]; ok {
			typeFilters["package"] = pkg
		}
		results, err := eng.ExactSearchPolyglot(ctx, wsID, typeFilters, 100)
		if err != nil {
			return false
		}
		if reading.importPath != "" {
			results = filterByImportPath(results, reading.importPath, root)
		}
		for _, r := range results {
			if metadata, _ := r.Point.Payload["metadata"].(map[string]interface{}); metadata["kind"] != "interface" && !hasEmbeddedField(metadata) {
				return true
			}
		}
	}
	return false
}

// hasEmbeddedField reports whether a Go struct has an embedded field, which
// the analyzer stores as a field without a name.
func hasEmbeddedField(metadata map[string]interface{}) bool {
	fields, _ := metadata["fields"].([]interface{})
	for _, raw := range fields {
		if field, _ := raw.(map[string]interface{}); field["name"] == "" {
			return true
		}
	}
	return false
}

func uniqueSymbol(results []storage.SearchResult, file string) (*storage.SearchResult, string) {
	if len(results) == 0 {
		return nil, "unresolved"
	}
	if file != "" {
		var local []storage.SearchResult
		for _, r := range results {
			path, _ := r.Point.Payload["file_path"].(string)
			if filepath.Clean(path) == filepath.Clean(file) {
				local = append(local, r)
			}
		}
		if len(local) > 0 {
			results = local
		}
	}
	if len(results) != 1 {
		return nil, "ambiguous"
	}
	return &results[0], "resolved"
}

func resolveCall(ctx context.Context, eng *engine.Engine, wsID string, source storage.SearchResult, rel map[string]interface{}) (*storage.SearchResult, string) {
	if id, _ := rel["target_id"].(string); id != "" {
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"symbol_id": id}, 2)
		if err != nil {
			return nil, "unresolved"
		}
		return uniqueSymbol(results, "")
	}
	file, _ := source.Point.Payload["file_path"].(string)
	name, _ := rel["target_name"].(string)
	language, _ := source.Point.Payload["language"].(string)
	receiver, _ := rel["receiver"].(string)
	if qualified, _ := rel["target_qualified_name"].(string); qualified != "" {
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": qualified}, 100)
		// Python self.helper() and PHP $this->helper() may call a method the
		// class inherits from a base class, mixin or trait.
		ownClass := (language == "python" && (receiver == "self" || receiver == "cls")) || (language == "php" && (receiver == "this" || receiver == "self"))
		if err == nil && len(results) == 0 && ownClass {
			results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"language": language, "type": "method", "name": name}, 100)
		}
		if err != nil {
			return nil, "unresolved"
		}
		return uniqueSymbol(results, file)
	}
	if receiver != "" {
		// Python utils.helper() calls a function of a project module named
		// utils, so json.dumps() does not reach a project's own dumps.
		if language == "python" {
			results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"language": "python", "type": "function", "name": name}, 100)
			if err != nil {
				return nil, "unresolved"
			}
			// import pkg.utils as u names the module pkg.utils by u, which
			// outside a package with __init__.py is indexed as utils.
			moduleNames := []string{receiver}
			imported, isImport := pythonImports(file)[receiver]
			if isImport {
				moduleNames = []string{imported.module, imported.module[strings.LastIndex(imported.module, ".")+1:]}
			}
			var module []storage.SearchResult
			for _, moduleName := range moduleNames {
				for _, r := range results {
					if pkg, _ := r.Point.Payload["package"].(string); inPythonModule(pkg, moduleName) {
						module = append(module, r)
					}
				}
				if len(module) > 0 {
					break
				}
			}
			// from db import session may bind a value, not a module.
			if len(module) > 0 || isImport && !imported.from {
				return uniqueSymbol(module, "")
			}
			// Any other receiver is a value of unknown type, so any method of
			// that name is a candidate; super() is not one of its own class.
			results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"language": "python", "type": "method", "name": name}, 100)
			if err != nil {
				return nil, "unresolved"
			}
			var methods []storage.SearchResult
			for _, r := range results {
				if receiver != "super()" || pythonClass(r) != pythonClass(source) {
					methods = append(methods, r)
				}
			}
			// Only super() narrows the type; one method of that name is no
			// proof that d.get() or os.path.join() calls it.
			if receiver != "super()" && len(methods) > 0 {
				return nil, "ambiguous"
			}
			return uniqueSymbol(methods, "")
		}
		if language != "go" {
			// The receiver's type is unknown, so any method of that name is a
			// candidate, unless a JS/TS Class.method() names another class.
			if language == "javascript" || language == "typescript" {
				results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"type": "method", "name": receiver + "." + name}, 2)
				if err != nil {
					return nil, "unresolved"
				}
				if len(results) > 0 {
					return uniqueSymbol(results, "")
				}
				// import * as money from './money' names money.ts's functions.
				results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"type": "function", "name": name, "package": receiver}, 2)
				if err != nil {
					return nil, "unresolved"
				}
				if len(results) > 0 {
					return uniqueSymbol(results, "")
				}
				// console.log() and Object.create() call a global or a class
				// the project does not index, not an instance of its classes.
				head, _, _ := strings.Cut(receiver, ".")
				if jsGlobals[head] {
					return nil, "unresolved"
				}
				if head != "" && unicode.IsUpper(rune(head[0])) {
					classes, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"type": "class", "name": head}, 1)
					if err != nil || len(classes) == 0 {
						return nil, "unresolved"
					}
				}
			}
			return nil, "ambiguous"
		}
		// Go records the receiver's type for calls on the method's own
		// receiver; that type lives in the caller's package. A promoted
		// method is not declared on it and falls back to the name.
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{
			"language": "go", "type": "method", "name": name,
			"metadata.receiver": receiver, "package": source.Point.Payload["package"],
		}, 2)
		if err != nil {
			return nil, "unresolved"
		}
		if len(results) > 0 {
			return uniqueSymbol(results, "")
		}
	}
	result, status := resolveSymbol(ctx, eng, wsID, "", name, file)
	// A Go call keeps only the method name, so the caller's file says nothing
	// about the receiver's type: only a workspace-unique method is an identity.
	if language == "go" && result != nil && result.Point.Payload["type"] == "method" {
		result, status = resolveSymbol(ctx, eng, wsID, "", name, "")
	}
	// New indexes distinguish free calls from member calls. Legacy indexes
	// without resolution metadata can still expose unique name-only edges.
	if _, indexed := rel["resolution"]; indexed && result != nil && result.Point.Payload["type"] == "method" && (language == "php" || language == "javascript" || language == "typescript") {
		return nil, "unresolved"
	}
	return result, status
}

// callMayTarget reports whether a call from source can reach target. A call
// that resolves elsewhere cannot; an ambiguous call by the target's name stays
// a candidate, except from the target itself: a name collision, not recursion.
func callMayTarget(source storage.SearchResult, rel map[string]interface{}, resolved *storage.SearchResult, status string, target storage.SearchResult) bool {
	if resolved != nil {
		return symbolKey(*resolved) == symbolKey(target)
	}
	// A member call reaches only a method, and in PHP and JS/TS a bare call
	// reaches no method, so JSON.parse() does not reach a project's parse().
	// A Python call on a module reaches that module's function.
	language, _ := source.Point.Payload["language"].(string)
	receiver, _ := rel["receiver"].(string)
	isMethod := target.Point.Payload["type"] == "method"
	pkg, _ := target.Point.Payload["package"].(string)
	if language != "go" && receiver != "" && !isMethod && !(language == "python" && inPythonModule(pkg, receiver)) {
		return false
	}
	if (language == "php" || language == "javascript" || language == "typescript") && receiver == "" && isMethod {
		return false
	}
	// JS/TS name a method Class.method; the call names only the method.
	name, _ := target.Point.Payload["name"].(string)
	return status == "ambiguous" && rel["target_name"] == shortSymbolName(name) && symbolKey(source) != symbolKey(target)
}

// jsGlobals are lowercase JS/TS globals; a capitalized one such as Object or
// JSON names no indexed class.
var jsGlobals = map[string]bool{
	"console": true, "window": true, "document": true, "globalThis": true, "process": true,
	"navigator": true, "location": true, "localStorage": true, "sessionStorage": true, "performance": true,
}

// inPythonModule reports whether a Python package is the module named, which
// may be the last part of its dotted name.
func inPythonModule(pkg, module string) bool {
	return pkg == module || strings.HasSuffix(pkg, "."+module)
}

func pythonClass(r storage.SearchResult) interface{} {
	metadata, _ := r.Point.Payload["metadata"].(map[string]interface{})
	return metadata["class"]
}

var (
	pyImportRe     = regexp.MustCompile(`^\s*import\s+(.+)`)
	pyFromImportRe = regexp.MustCompile(`^\s*from\s+(\S+)\s+import\s+(.+)`)
)

// pythonImport is the dotted path a name is imported from; a from-import may
// bind a class or another value rather than a module.
type pythonImport struct {
	module string
	from   bool
}

// pythonImports maps each name a Python file's imports bind to the module it
// names: import a.b binds a and b, while import a.b as m and from a import b
// as m bind m to a.b.
func pythonImports(path string) map[string]pythonImport {
	imports := map[string]pythonImport{}
	// Never open a FIFO or device that took an indexed file's place.
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return imports
	}
	f, err := os.Open(path)
	if err != nil {
		return imports
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 1<<20)
	from, inParens := "", false
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		names, isFrom := "", inParens
		if inParens {
			names = line
		} else if m := pyFromImportRe.FindStringSubmatch(line); m != nil {
			from, names, isFrom = strings.Trim(m[1], "."), m[2], true
		} else if m := pyImportRe.FindStringSubmatch(line); m != nil {
			names = m[1]
		}
		// from a import (b,\n c) continues on the next lines.
		if isFrom {
			inParens = (inParens || strings.Contains(names, "(")) && !strings.Contains(names, ")")
		}
		for _, part := range strings.Split(names, ",") {
			fields := strings.Fields(strings.Trim(part, " \t()\\"))
			if len(fields) == 0 {
				continue
			}
			module := fields[0]
			if isFrom {
				module = strings.Trim(from+"."+module, ".")
			}
			if len(fields) == 3 && fields[1] == "as" {
				imports[fields[2]] = pythonImport{module, isFrom}
				continue
			}
			if isFrom {
				imports[fields[0]] = pythonImport{module, true}
				continue
			}
			segments := strings.Split(module, ".")
			for i, segment := range segments {
				imports[segment] = pythonImport{module: strings.Join(segments[:i+1], ".")}
			}
		}
	}
	return imports
}

func symbolKey(r storage.SearchResult) string {
	if id, _ := r.Point.Payload["symbol_id"].(string); id != "" {
		return id
	}
	return r.Point.ID
}

func shortSymbolName(name string) string {
	// Scene symbols and dependency targets include their extension; the
	// dot separates the filename, rather than a class and member name.
	if strings.EqualFold(filepath.Ext(name), ".tscn") {
		return name
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+2:]
	}
	// PHP namespaces: App\Models\User is referenced as User.
	if i := strings.LastIndex(name, "\\"); i >= 0 {
		return name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}
