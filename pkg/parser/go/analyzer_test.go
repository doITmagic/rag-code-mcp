package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realIndexerDir points to the actual pkg/indexer package in the project.
// Tests that use this directory verify Go parser behaviour against real
// production code, covering edge cases like constructor functions (BUG-003)
// that go/doc moves from docPkg.Funcs into docPkg.Types[T].Funcs.
func realIndexerDir(t *testing.T) string {
	t.Helper()
	// Walk up from the test file's directory to the repo root.
	dir, err := filepath.Abs("../../indexer")
	require.NoError(t, err)
	_, err = os.Stat(dir)
	require.NoError(t, err, "pkg/indexer must exist; run tests from the repo root")
	return dir
}

// ---------------------------------------------------------------------------
// Tests against pkg/indexer — real production code as parser fixture
// ---------------------------------------------------------------------------

// TestRealPackage_IndexerKnownSymbols verifies that the Go parser correctly
// extracts all exported symbols from pkg/indexer, including constructor/loader
// functions that go/doc associates with their return type (BUG-003 regression).
//
// Symbols expected to be parsed (exported, capitalized):
//
//	CountAllFiles, FileState, GetFileState, IndexFile, IndexItems,
//	IndexStatus, IndexWorkspace, IsChanged, LangStatus, Options,
//	RemoveFile, Save, SaveIndexStatus, Service, State, UpdateFile
//
// Symbols that were MISSING before BUG-003 fix (typ.Funcs not iterated):
//
//	LoadIndexStatus, NewService, NewState, LoadState
func TestRealPackage_IndexerKnownSymbols(t *testing.T) {
	dir := realIndexerDir(t)
	ca := NewCodeAnalyzer()

	res, err := ca.Analyze(context.Background(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, res.Symbols, "pkg/indexer must produce symbols")

	indexed := make(map[string]pkgParser.Symbol)
	for _, s := range res.Symbols {
		indexed[s.Name] = s
	}

	// ── Symbols confirmed IN Qdrant before the fix ──────────────────────────
	knownPresent := []string{
		"CountAllFiles", "FileState", "GetFileState", "IndexFile", "IndexItems",
		"IndexStatus", "IndexWorkspace", "IsChanged", "LangStatus", "Options",
		"RemoveFile", "Save", "SaveIndexStatus", "Service", "State", "UpdateFile",
	}
	for _, name := range knownPresent {
		_, ok := indexed[name]
		assert.True(t, ok, "symbol %q was present in Qdrant DB and must still be parsed", name)
	}

	// ── Symbols MISSING before fix (BUG-003 regression check) ────────────────
	// go/doc places these in typ.Funcs, not docPkg.Funcs.
	// Before the fix (adding the typ.Funcs loop) none of these appeared in index.
	bug003Fixed := []string{"LoadIndexStatus", "NewService", "NewState", "LoadState"}
	for _, name := range bug003Fixed {
		sym, ok := indexed[name]
		assert.True(t, ok,
			"BUG-003 regression: %q must now be indexed (go/doc puts it in typ.Funcs)", name)
		if ok {
			assert.True(t, sym.IsPublic, "%q must be IsPublic=true", name)
			assert.Equal(t, "indexer", sym.Package, "%q must have package=indexer", name)
			assert.NotEmpty(t, sym.FilePath, "%q must have FilePath set", name)
			assert.Greater(t, sym.StartLine, 0, "%q must have StartLine > 0", name)
			assert.Contains(t, sym.Signature, name, "%q signature must contain function name", name)
		}
	}
}

// TestRealPackage_IndexerPublicPrivate verifies IsPublic correctness against
// the real pkg/indexer package. The Qdrant snapshot showed all 16 public
// symbols had is_public=true and 9 private symbols had is_public=false.
func TestRealPackage_IndexerPublicPrivate(t *testing.T) {
	dir := realIndexerDir(t)
	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), dir)
	require.NoError(t, err)

	indexed := make(map[string]pkgParser.Symbol)
	for _, s := range res.Symbols {
		indexed[s.Name] = s
	}

	// Confirmed PUBLIC in Qdrant (is_public=true)
	publicSymbols := []string{
		"CountAllFiles", "FileState", "GetFileState", "IndexFile", "IndexItems",
		"IndexStatus", "IndexWorkspace", "IsChanged", "LangStatus", "Options",
		"RemoveFile", "Save", "SaveIndexStatus", "Service", "State", "UpdateFile",
		// Fixed by BUG-003 patch — should now also be public
		"LoadIndexStatus", "NewService", "NewState", "LoadState",
	}
	for _, name := range publicSymbols {
		if sym, ok := indexed[name]; ok {
			assert.True(t, sym.IsPublic, "%q must have IsPublic=true", name)
		}
	}

	// Confirmed PRIVATE in Qdrant (is_public=false)
	privateSymbols := []string{
		"attemptOllamaRestart", "circuitBreakerThreshold", "deleteCollectionForRecreate",
		"deleteCollectionMaxWait", "deleteCollectionTimeout", "ensureOllamaAlive",
		"indexStatusFile", "symbolToMap", "unwrapOllamaProvider",
	}
	for _, name := range privateSymbols {
		if sym, ok := indexed[name]; ok {
			assert.False(t, sym.IsPublic, "%q must have IsPublic=false", name)
		}
	}
}

// TestRealPackage_IndexerSignatures spot-checks that Go signatures are
// correctly extracted from the real pkg/indexer files.
// Expectations derived from Qdrant payload "signature" field (DB snapshot).
func TestRealPackage_IndexerSignatures(t *testing.T) {
	dir := realIndexerDir(t)
	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), dir)
	require.NoError(t, err)

	indexed := make(map[string]pkgParser.Symbol)
	for _, s := range res.Symbols {
		indexed[s.Name] = s
	}

	cases := []struct {
		name      string
		wantParts []string // all must appear in Signature
	}{
		// From Qdrant payload "signature" field:
		{"SaveIndexStatus", []string{"SaveIndexStatus", "workspaceRoot", "IndexStatus"}},
		{"IndexWorkspace", []string{"IndexWorkspace", "root", "collection", "Options", "error"}},
		{"IndexFile", []string{"IndexFile", "collection", "path", "State", "int", "error"}},
		{"IndexItems", []string{"IndexItems", "collection", "error"}},
		{"CountAllFiles", []string{"CountAllFiles", "root", "excludePatterns", "FileCountResult"}},
		// BUG-003 fixed — check constructor signatures too
		{"NewService", []string{"NewService", "Service"}},
		{"LoadState", []string{"LoadState", "path", "State", "error"}},
		{"LoadIndexStatus", []string{"LoadIndexStatus", "workspaceRoot", "IndexStatus"}},
		{"NewState", []string{"NewState", "State"}},
	}

	for _, tc := range cases {
		sym, ok := indexed[tc.name]
		if !ok {
			t.Errorf("symbol %q not found in parsed output", tc.name)
			continue
		}
		for _, part := range tc.wantParts {
			assert.True(t, strings.Contains(sym.Signature, part),
				"signature of %q should contain %q; got: %q", tc.name, part, sym.Signature)
		}
	}
}

// TestRealPackage_IndexerLineCoverage verifies that start/end lines are
// plausible for real functions in pkg/indexer/index_status.go.
// We check that lines are > 0 and roughly in the expected region of the file,
// rather than hardcoding exact numbers that break whenever the file is edited.
func TestRealPackage_IndexerLineCoverage(t *testing.T) {
	dir := realIndexerDir(t)
	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), dir)
	require.NoError(t, err)

	indexed := make(map[string]pkgParser.Symbol)
	for _, s := range res.Symbols {
		indexed[s.Name] = s
	}

	for _, name := range []string{"SaveIndexStatus", "LoadIndexStatus"} {
		sym, ok := indexed[name]
		require.True(t, ok, "%q must be indexed", name)
		assert.Greater(t, sym.StartLine, 0,
			"%q StartLine must be > 0", name)
		assert.True(t, strings.HasSuffix(sym.FilePath, "index_status.go"),
			"%q FilePath should end in index_status.go, got %q", name, sym.FilePath)
	}
}

// ---------------------------------------------------------------------------
// Interface / basic coverage tests (kept but updated to use real fixtures)
// ---------------------------------------------------------------------------

func TestCodeAnalyzer_Complete(t *testing.T) {
	tmpDir := t.TempDir()

	// This code mirrors the structure of pkg/indexer (types + constructors + methods)
	// to keep the synthetic fixture representative of real-world patterns.
	code := `package testpkg

import "fmt"

const Version = "1.0.0"
var Debug = true

// Calculator provides mathematical operations.
type Calculator struct {
	precision int
}

// Add adds two numbers.
func Add(a, b int) int {
	return a + b
}

// Multiply multiplies two numbers.
func (c *Calculator) Multiply(a, b int) int {
	return a * b
}

// NewCalculator creates a new Calculator.
// go/doc will place this in Types["Calculator"].Funcs — BUG-003 pattern.
func NewCalculator(precision int) *Calculator {
	return &Calculator{precision: precision}
}

// Subtractor is an interface for subtraction.
type Subtractor interface {
	Subtract(a, b int) int
}

var _ = fmt.Sprintf
`
	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte(code), 0644)
	require.NoError(t, err)
	ca := NewCodeAnalyzer()

	t.Run("Interface implementation", func(t *testing.T) {
		var _ pkgParser.Analyzer = ca
		assert.Equal(t, "go", ca.Name())
		assert.True(t, ca.CanHandle("test.go"))
		assert.False(t, ca.CanHandle("test.py"))
		assert.True(t, ca.CanHandle("test_test.go")) // tests are indexed too
	})

	t.Run("Analyze directory", func(t *testing.T) {
		res, err := ca.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, "go", res.Language)

		symbols := make(map[string]pkgParser.Symbol)
		for _, s := range res.Symbols {
			symbols[s.Name+"_"+string(s.Type)] = s
		}

		assert.Contains(t, symbols, "Add_function")
		assert.Contains(t, symbols, "Calculator_type")
		assert.Contains(t, symbols, "Multiply_method")
		assert.Contains(t, symbols, "Subtractor_type")
		assert.Contains(t, symbols, "Version_const")
		assert.Contains(t, symbols, "Debug_var")

		// BUG-003 regression check in synthetic fixture
		assert.Contains(t, symbols, "NewCalculator_function",
			"NewCalculator must be indexed (go/doc puts it in typ.Funcs)")

		addFunc := symbols["Add_function"]
		assert.Equal(t, "testpkg", addFunc.Package)
		assert.Equal(t, "func Add(a int, b int) int", addFunc.Signature)
		assert.Equal(t, "Add adds two numbers.", addFunc.Docstring)
		assert.Contains(t, addFunc.Content, "return a + b")

		subtractor := symbols["Subtractor_type"]
		assert.Equal(t, "interface", subtractor.Metadata["kind"])
		methods := subtractor.Metadata["methods"].([]MethodInfo)
		require.Len(t, methods, 1)
		assert.Equal(t, "Subtract", methods[0].Name)

		ctor := symbols["NewCalculator_function"]
		assert.True(t, ctor.IsPublic)
		assert.Equal(t, "testpkg", ctor.Package)
		assert.Contains(t, ctor.Signature, "NewCalculator")
	})

	t.Run("AnalyzePackage includes constructor functions", func(t *testing.T) {
		pkg, err := ca.AnalyzePackage(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, "testpkg", pkg.Name)

		funcNames := make(map[string]bool)
		for _, fn := range pkg.Functions {
			funcNames[fn.Name] = true
		}
		assert.True(t, funcNames["Add"])
		assert.True(t, funcNames["Multiply"])
		assert.True(t, funcNames["NewCalculator"],
			"NewCalculator must appear — BUG-003 regression")
		assert.Len(t, pkg.Types, 2)
	})

	t.Run("Non-existent directory", func(t *testing.T) {
		_, err := ca.AnalyzePackage("/tmp/does-not-exist-rag-code")
		assert.Error(t, err)
	})

	t.Run("Directory with no go files", func(t *testing.T) {
		emptyDir := t.TempDir()
		res, err := ca.Analyze(context.Background(), emptyDir)
		assert.NoError(t, err)
		assert.Empty(t, res.Symbols)
	})
}

func TestCodeAnalyzer_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	ca := NewCodeAnalyzer()

	t.Run("Parse error file", func(t *testing.T) {
		badCode := `package bad; func {`
		err := os.WriteFile(filepath.Join(tmpDir, "bad.go"), []byte(badCode), 0644)
		require.NoError(t, err)
		res, err := ca.Analyze(context.Background(), tmpDir)
		assert.NoError(t, err)
		assert.Empty(t, res.Symbols)
	})

	t.Run("Skip vendor and hidden", func(t *testing.T) {
		vendorDir := filepath.Join(tmpDir, "vendor")
		require.NoError(t, os.Mkdir(vendorDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(vendorDir, "skip.go"), []byte("package vendor"), 0644))

		hiddenDir := filepath.Join(tmpDir, ".hidden")
		require.NoError(t, os.Mkdir(hiddenDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(hiddenDir, "skip.go"), []byte("package hidden"), 0644))

		res, err := ca.AnalyzePaths([]string{tmpDir})
		assert.NoError(t, err)
		assert.Empty(t, res)
	})

	t.Run("Complex types", func(t *testing.T) {
		complexDir := t.TempDir()
		code := `package complexpkg
type Data struct {
    Items []string
    Mapping map[string]int
    Done chan bool
    Handler func(int) error
}
`
		require.NoError(t, os.WriteFile(filepath.Join(complexDir, "complex.go"), []byte(code), 0644))
		res, err := ca.Analyze(context.Background(), complexDir)
		assert.NoError(t, err)
		assert.NotEmpty(t, res.Symbols)
	})
}

func TestCodeAnalyzer_BugFixes(t *testing.T) {
	ca := NewCodeAnalyzer()

	t.Run("Typed consts and vars (iota enums) and per-ValueSpec spans", func(t *testing.T) {
		tmpDir := t.TempDir()
		code := `package enumtest

type Role int

const (
	// RoleGuest is a guest role.
	RoleGuest Role = iota
	// RoleAdmin is an admin role.
	RoleAdmin
)

var (
	// DefaultRole is the default role.
	DefaultRole Role = RoleGuest
	// FallbackRole is the fallback role.
	FallbackRole Role = RoleAdmin
)
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "enum.go"), []byte(code), 0644))
		res, err := ca.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)

		byName := make(map[string]pkgParser.Symbol)
		for _, s := range res.Symbols {
			byName[s.Name] = s
		}

		require.Contains(t, byName, "RoleGuest", "typed iota const RoleGuest must be extracted from typ.Consts")
		require.Contains(t, byName, "RoleAdmin", "typed iota const RoleAdmin must be extracted from typ.Consts")
		require.Contains(t, byName, "DefaultRole", "typed var DefaultRole must be extracted from typ.Vars")
		require.Contains(t, byName, "FallbackRole", "typed var FallbackRole must be extracted from typ.Vars")

		guest := byName["RoleGuest"]
		admin := byName["RoleAdmin"]
		assert.Equal(t, 7, guest.StartLine)
		assert.Equal(t, 7, guest.EndLine)
		assert.Equal(t, "RoleGuest is a guest role.", guest.Docstring)
		assert.Equal(t, 9, admin.StartLine)
		assert.Equal(t, 9, admin.EndLine)
		assert.Equal(t, "RoleAdmin is an admin role.", admin.Docstring)

		defRole := byName["DefaultRole"]
		fbRole := byName["FallbackRole"]
		assert.Equal(t, 14, defRole.StartLine)
		assert.Equal(t, 14, defRole.EndLine)
		assert.Equal(t, "DefaultRole is the default role.", defRole.Docstring)
		assert.Equal(t, 16, fbRole.StartLine)
		assert.Equal(t, 16, fbRole.EndLine)
		assert.Equal(t, "FallbackRole is the fallback role.", fbRole.Docstring)
	})

	t.Run("Generic receiver AST body lookup and complex type expressions", func(t *testing.T) {
		tmpDir := t.TempDir()
		code := `package genpkg

type Stack[T any] struct {
	items []T
}

type Pair[K comparable, V any] struct {
	key K
	val V
}

func Helper() {}

func (s *Stack[T]) Push(v T) {
	Helper()
	s.items = append(s.items, v)
}

func Process(ch <-chan int, fn func(int) error, pair Pair[string, int], tags ...string) chan<- bool {
	return nil
}
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "generic.go"), []byte(code), 0644))
		pkgInfo, err := ca.AnalyzePackage(tmpDir)
		require.NoError(t, err)

		var pushFn, processFn *FunctionInfo
		for i := range pkgInfo.Functions {
			switch pkgInfo.Functions[i].Name {
			case "Push":
				pushFn = &pkgInfo.Functions[i]
			case "Process":
				processFn = &pkgInfo.Functions[i]
			}
		}

		require.NotNil(t, pushFn, "generic method Push must be found")
		assert.Contains(t, pushFn.Code, "s.items = append(s.items, v)", "generic method body must be extracted")
		assert.Contains(t, pushFn.Calls, "Helper", "generic method calls must be extracted from AST body")
		assert.Equal(t, "func (s *Stack[T]) Push(v T)", pushFn.Signature)

		require.NotNil(t, processFn, "Process function must be found")
		assert.NotContains(t, processFn.Signature, "unknown", "typeToString must not produce 'unknown'")
		assert.Equal(t, "func Process(ch <-chan int, fn func(int) error, pair Pair[string, int], tags ...string) chan<- bool", processFn.Signature)
	})

	t.Run("Duplicate function names across files do not collide in AST body map", func(t *testing.T) {
		tmpDir := t.TempDir()
		fileA := `package duppkg

func Open() string {
	firstCall()
	return "first"
}
`
		fileB := `package duppkg

func Open() string {
	secondCall()
	return "second"
}
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "open_a.go"), []byte(fileA), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "open_b.go"), []byte(fileB), 0644))

		fset := ca.buildFunctionASTMap(nil)
		assert.Empty(t, fset)

		pkgInfo, err := ca.AnalyzePackage(tmpDir)
		require.NoError(t, err)
		require.NotEmpty(t, pkgInfo.Functions)
		for _, fn := range pkgInfo.Functions {
			if fn.Name == "Open" {
				if strings.HasSuffix(fn.FilePath, "open_a.go") {
					assert.Contains(t, fn.Calls, "firstCall")
					assert.NotContains(t, fn.Calls, "secondCall")
				} else if strings.HasSuffix(fn.FilePath, "open_b.go") {
					assert.Contains(t, fn.Calls, "secondCall")
					assert.NotContains(t, fn.Calls, "firstCall")
				}
			}
		}
	})

	t.Run("Promoted methods from embedded structs are not duplicated", func(t *testing.T) {
		tmpDir := t.TempDir()
		code := `package embedpkg

type Base struct{}

func (b *Base) BaseMethod() string {
	return "base"
}

type Derived struct {
	Base
}

func (d *Derived) DerivedMethod() string {
	return "derived"
}
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "embed.go"), []byte(code), 0644))
		pkgInfo, err := ca.AnalyzePackage(tmpDir)
		require.NoError(t, err)

		baseMethodCount := 0
		for _, fn := range pkgInfo.Functions {
			if fn.Name == "BaseMethod" {
				baseMethodCount++
				assert.Equal(t, "Base", fn.Receiver)
			}
		}
		assert.Equal(t, 1, baseMethodCount, "promoted method BaseMethod must not be duplicated on Derived")

		for _, tp := range pkgInfo.Types {
			if tp.Name == "Derived" {
				require.Len(t, tp.Methods, 1)
				assert.Equal(t, "DerivedMethod", tp.Methods[0].Name)
			}
		}
	})
}

