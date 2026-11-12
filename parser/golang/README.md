[RagCode](../../README.md) › [parser](../README.md) › golang

# Go Analyzer

The `golang` package turns one Go source file into `parser.Symbol` values: one
symbol per top-level declaration (function, method, type, var, const), with
call (including functions and methods named as values), type-use and
embedding relations resolved through the file's own import
table and the types it declares, plus one `route` symbol per HTTP route
registration in a file that imports a router ([Routes](#routes)).

It uses the tree-sitter `go` grammar (through the shared helpers in
[`parser/ts.go`](../README.md#tree-sitter-helpers-tsgo)), not `go/ast` or
`go/types`. That keeps it fast and tolerant of broken files, at the cost of
having no type information: a call on a variable whose type is unknown keeps
an empty `TargetQualifiedName`.

## Where it sits

- **Registered** from `init()` with `parser.Register(Analyzer{})`; the binary
  pulls it in through a blank import in
  [`internal/app`](../../internal/app/README.md).
- **Called** by [`index`](../../index/README.md) (and by
  [`tools`](../../tools/README.md) for live parsing) through `parser.Analyze`.
- **Calls** `parser.ParseTree`, `parser.Walk`, `parser.Finalize` and the other
  helpers from [parser](../README.md). It reads `go.mod` files from disk to
  compute the module path.

| Property | Value |
|----------|-------|
| Language | `go` |
| Extensions | `.go` |
| Exported type | `Analyzer` (stateless struct) |

## How a file is analyzed

```mermaid
graph TD
    A[parser.File] --> B[ParseTree go]
    B --> C[Pass 1: import table<br/>+ type names declared in the file]
    C --> D[Pass 2: one symbol per<br/>top-level declaration]
    D --> E[relations per declaration]
    E --> F[parser.Finalize]
    G[modulePath: nearest go.mod<br/>at or above the dir, not above Root] --> D
```

1. **Pass 1** reads every `import_spec` into a map `local name -> import path`
   (an alias replaces the assumed name; dot and blank imports are skipped)
   and records every type name declared in the file. The assumed name follows
   goimports: the last path element, the one before a `/vN` major suffix,
   without a `go-` prefix and cut at the first non-identifier character, so
   `gopkg.in/yaml.v3` is `yaml` and `github.com/mattn/go-isatty` is `isatty`.
2. **Pass 2** emits symbols for `function_declaration`, `method_declaration`,
   `type_declaration`, `var_declaration` and `const_declaration`.
3. **Module** is the Go import path: the `module` line of the nearest `go.mod`
   at or above the file's directory (never above the workspace root) plus the
   relative directory. A `go.mod` that is not a regular file whose real path
   (symlinks resolved) is inside the root counts as absent. Without a `go.mod`
   it is the directory relative to the root; a file directly in the root has module `""`. A `_test.go` file of an
   external test package (`package flux_test`) gets that module plus `_test`,
   so its declarations never share qualified names with `package flux`.

## Symbols extracted

| Declaration | Kind | Owner | Notes |
|-------------|------|-------|-------|
| `func F()` | `function` | | Signature is the source up to the body. |
| `func (r *T) M()` | `method` | `T` | Owner is the receiver base type with pointer and type parameters stripped. |
| `type T struct{...}` | `class` | | |
| `type I interface{...}` | `interface` | | `Metadata["methods"]` lists the interface's method names, comma separated. |
| `type A = B`, `type N []string` | `type` | | Any other type spec, aliases included. |
| `var x, y = ...` | `variable` | | One symbol per name; `_` is skipped unless the spec has relations (`var _ = Describe(...)` in Ginkgo tests, `var _ I = (*T)(nil)`): their calls and type uses would be lost otherwise. |
| `const ...` | `constant` | | One symbol per name; `_` is skipped unless the spec has relations, as for `var`. |

- **Grouped declarations** (`type (...)`, `var (...)`, `const (...)`): each
  spec becomes its own symbol whose content is just that spec; a declaration
  with a single spec uses the whole declaration (keyword included).
- **Doc** is the comment block ending on the line right above the declaration,
  markers removed (`//`, `/* */`, leading `*`); `//go:` directives are dropped.
- **Exported** is true when the name starts with an upper-case letter, the
  owner (for methods) is exported too, and the file is not a `_test.go` file.

### Example

With `go.mod` saying `module github.com/acme/app` and the file at
`internal/svc/svc.go`:

```go
package svc

import str "strings"

// Server doc.
type Server struct {
	Base
	Name string
}

func (s *Server) Start() {
	s.Stop()
	str.ToUpper("a")
}
```

| Qualified name | Kind | Owner |
|----------------|------|-------|
| `github.com/acme/app/internal/svc.Server` | `class` | |
| `github.com/acme/app/internal/svc.Server.Start` | `method` | `Server` |

Relations: `Server` extends `Base` -> `github.com/acme/app/internal/svc.Base`
(an unqualified type is declared in the same package, in this file or
another); `Start` calls `Stop` ->
`github.com/acme/app/internal/svc.Server.Stop` and `ToUpper` ->
`strings.ToUpper`.

## Relations extracted

| Source construct | Relation | How the target is resolved |
|------------------|----------|----------------------------|
| `f(x)` | `call` | `<module>.f`. Built-ins (`len`, `append`, `make`, conversion type names, ...) and names declared as locals or parameters in the same declaration are skipped. |
| `T(x)` with `T` declared in the file | `uses_type` | Conversion to a local type: `<module>.T`. |
| `pkg.F(x)` | `call` | Through the import table: `<import path>.F`, unless a parameter or local named `pkg` shadows the import (then `""`). |
| `recv.M()` inside a method | `call` | Receiver variable: `<module>.<Owner>.M`. |
| `T.M(...)`, `(*T).M(...)`, `T{}.M()` | `call` | Type declared in the file: `<module>.T.M`. |
| `x.M()` on anything else | `call` | Target name only, qualified name `""`. |
| `F[int](x)` / `pkg.F[T](x)` | `call` (or `uses_type` when `F` is a local type) | Generic calls parse as conversions; classified by whether `F` is a type declared here. |
| `T` in any type position (field, parameter, result, `var`, composite literal, `new(T)`, type switch) | `uses_type` | `<module>.T`: not predeclared (`int`, `error`, `any`, `comparable`, ...) and not a type parameter, so declared in the same package, in this file or another. The declared name of a type and a method's receiver type are not uses. |
| `pkg.T` in any type position | `uses_type` | `<import path>.T`, or `""` when `pkg` is not imported. |
| Embedded struct field | `extends` | Same-package type or imported type. |
| Embedded interface (`fmt.Stringer`) | `extends` | Same rule. |
| `f`, `recv.M`, `s.M`, `T.M`, `pkg.F` named as a value: a call argument, the right side of `=`, `:=` or `var`, a composite literal value (keyed or not, never the key), a `return` value | `call` with `Value: true` | The target a call on the same expression gets (rows above), plus `s.M` on a local declared with one type (`s *T`, `var s T`, `s := &T{...}`): `<module>.T.M`. Not emitted: an operand of unknown type (no qualified target, so it could never link exactly), and what the file shows is no function (built-ins, locals and parameters, types, top-level vars and consts declared here, a selector named like a struct field declared here and no method here). The index keeps the relation only when it links exactly to a function or method ([index](../../index/README.md#6-resolve)). |

Relations of a `var` or `const` spec are shared by every name in that spec.

## Routes

A file that imports an HTTP router gets one `route` symbol per registration
call in its functions and methods, as PHP does for Laravel. The import is the
evidence: a method named `GET` in a file that imports no router is no route.
`_test.go` files produce none (a test's router setup is not the app's).

| Framework | Import | Registration calls (path argument) | Group |
|-----------|--------|-------------------------------------|-------|
| Echo | `github.com/labstack/echo` (any `/vN`) | `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `OPTIONS`, `HEAD`, `CONNECT`, `TRACE`, `Any`, `Static`, `File` (first); `Match`, `Add` (second) | `Group(prefix, ...)` |

- The path argument must be a string literal that is `""` or starts with
  `/`, and the argument after it must not be a string literal (except for
  `Static` / `File`, whose second argument is a directory or file), so
  `Header().Add("X-Key", "v")` and `time.Now().Add(d)` are no routes.
- **Name** is the full path: the group prefixes the file shows joined in front
  (`g := e.Group("/admin")`, `v1 := g.Group("/v1")`, `s.api = e.Group("/api")`,
  `e.Group("/u").Static("/", dir)`); an empty result is `/`. A group variable
  passed to a function of the same file gives that function's parameter its
  prefix when every call in the file passes the same one; otherwise, and for
  any router whose prefix the file does not show (a parameter, a field set
  elsewhere), the path stays as written.
- **Owner** is the enclosing function or method name (as PHP routes take
  the enclosing symbol's name), so `admin.GET("/users", h)` in `func Load`
  of `example.com/app/routes` is `example.com/app/routes.Load./admin/users`.
  A `GET` and a `POST` of one path in one function share the qualified name
  (twins, one target).
- **Metadata**: `framework=echo`, `method=<the call's method name>`.
  Content is the call expression, signature its first line, `Exported` true.
- **Relation**: the handler argument gets the `call` relation with
  `Value: true` that the enclosing function already has for it (a function
  name, a method value on the receiver or on a local of known type, a
  package function), so `rag_find_usages` of the handler lists the route.
  An inline `func(c echo.Context) error {...}`, a method value on an operand
  of unknown type (`c := NewController(); e.GET(p, c.Index)`) and
  `Static` / `File` have no handler relation.

## Known limitations visible in the code

- No type inference: calls on variables, fields or return values
  (`s.Inner.Do()`, `a.Do()`) keep an empty target qualified name.
- Function and method values: only the positions in the table count. Not
  covered: a method value on an operand of unknown type (a local from a
  function call, `s := newServer(); mux.HandleFunc(p, s.h)`, a package-level
  var, a field `a.b.h`); a method expression `T.m` / `(*T).m` whose type is
  declared in another file; a value sent on a channel, put in parentheses or
  reached through an interface; a function value stored in a variable and
  called later is a usage where it is named, not where it is called. A file
  at a workspace root without `go.mod` has an empty module, so its value
  uses have no qualified target the index can link exactly.
- A value use whose target is declared in another file is linked only when
  both files are in the same index run (or the target is already stored):
  the index drops it otherwise, so a handler file added after the file that
  registers it shows the usage only once the registering file changes or a
  full re-index runs.
- A local variable shadows a package function, an import or the method's
  receiver of the same name across the whole declaration, not only after its
  declaration (marked as a `shortcut:` in `locals`); a call on a shadowed
  receiver keeps an empty target.
- The `go.mod` lookup is cached per root + directory and re-read when that
  `go.mod` changes (mtime) or disappears; a `go.mod` created later in a
  directory that had none needs a restart to show up. A `go.mod` symlinked
  out of the workspace root is ignored, so the module falls back to the
  directory path.
- Methods whose receiver type cannot be read produce no symbol.
- Routes: only the frameworks in [Routes](#routes) are read. Routes
  registered through a project's own helper or a table of structs, a group
  passed to a method (`s.register(g)`) and a path built at run time
  (`"/v" + n`) produce no route or keep the path as written.
- Interface methods are not symbols of their own: they are only listed in the
  interface's `Metadata["methods"]`. `rag_find_usages` on a method declared
  only in an interface (no implementation) answers "no indexed symbol named";
  calls through the interface (`repo.M()`) stay unresolved (`x.M()` rule).
- An import without an alias is known by the goimports guess of its name
  (`assumedName`), not by the imported package's `package` clause; a
  package whose real name differs from that guess gets calls with an empty
  target, because only the file being parsed is read.

## Files

| File | Contents |
|------|----------|
| `golang.go` | `Analyzer` (`Language`, `Extensions`, `Analyze`), the per-file context, symbol builders (`appendFunc`, `appendTypes`, `appendValues`), relation collection (`relations`, `callTarget`, `genericCall`, `resolveType`), doc comment extraction and the cached `go.mod` module-path lookup. |
| `routes.go` | The router table by import path (`frameworks`), `routes` / `declRoutes` (group prefixes per declaration, carried into same-file functions), `route` (one registration call), `handlerRelation`. |

### Tests

| File | Covers |
|------|--------|
| `golang_test.go` | A fixture with grouped declarations, generics, embedded types, aliased/dot/blank imports: symbol kinds, owners, lines, signatures, docs, IDs, metadata; every relation and its target; module path without `go.mod`, nested `go.mod` stopping at the root; malformed input; analyzing the real `parser.go` of this repo; registration; concurrency; generic calls; `Exported` for test files and unexported owners; calls through local function values; a parameter shadowing an import; a local shadowing the receiver; the line of a chained call split over lines; functions and methods named as values (arguments, assignments, literals, returns), with fields, vars, consts, type switch and select locals skipped; an external test package as its own module; a renamed module seen without a restart; same-package types declared in another file, type parameters (receiver ones too) and the receiver skipped; blank vars keeping their relations; goimports-style assumed import names; a `go.mod` symlinked out of the root ignored, one symlinked inside it read. |
| `routes_test.go` | Echo routes: nested and inline groups, a group in a struct field, a group passed to a function (and the prefix left out when two calls disagree), each handler form (function, method value, package function, inline closure), `Match` / `Add` / `Static` / `File`; no route in a file without a router import, in a test file, or for `Header().Add` / `time.Now().Add`. |
| `zz_verify_test.go` | Verification tests: resolution probes, an unclean (non-normalized) root path, robustness on garbage and edge input, a second round of edge cases, very large and deeply nested files. |

## Testing

```bash
go test ./parser/golang/
```

No build tags or external services. Tests create temporary `go.mod` files
with `t.TempDir()`.

## See also

- [parser](../README.md): symbol model, qualified names, registry
- [index](../../index/README.md): resolves `TargetQualifiedName` to symbol IDs
- Other analyzers: [python](../python/README.md), [javascript](../javascript/README.md), [php](../php/README.md)
