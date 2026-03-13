# Parser Separation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extract CSS, SQL, Shell/Bash, and Svelte file types from the "docs" parser into dedicated parsers, each with its own language name and Qdrant collection.

**Architecture:** Each new parser package (`css`, `sql`, `shell`, `svelte`) registers itself via `init()`, delegates chunking to the existing `docs.TreeSitterParser`, and returns its own `Language` name. The `docs` analyzer loses 5 extensions (`.css`, `.scss`, `.sql`, `.sh`, `.svelte`). The daemon's `run.go` gains 4 blank imports to trigger registration.

**Tech Stack:** Go, gotreesitter v0.6.0 (css/scss/sql/bash/svelte grammars already bundled), testify

---

## Task 1: Create `pkg/parser/css/analyzer.go`

**Files:**
- Create: `pkg/parser/css/analyzer.go`
- Create: `pkg/parser/css/analyzer_test.go`

**Step 1: Write the failing test**

Create `pkg/parser/css/analyzer_test.go`:

```go
package css_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/css"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_CanHandle(t *testing.T) {
	a := css.NewAnalyzer()
	assert.True(t, a.CanHandle("style.css"))
	assert.True(t, a.CanHandle("main.scss"))
	assert.False(t, a.CanHandle("script.js"))
	assert.False(t, a.CanHandle("query.sql"))
}

func TestAnalyzer_Name(t *testing.T) {
	assert.Equal(t, "css", css.NewAnalyzer().Name())
}

func TestAnalyzer_ParseCSS(t *testing.T) {
	a := css.NewAnalyzer()
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "style.css")
	require.NoError(t, os.WriteFile(f, []byte(`
body { background: red; }
.header { font-size: 24px; }
`), 0644))

	result, err := a.Analyze(context.Background(), f)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "css", result.Language)
	assert.Greater(t, len(result.Symbols), 0)
}

func TestAnalyzer_ParseSCSS(t *testing.T) {
	a := css.NewAnalyzer()
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "main.scss")
	require.NoError(t, os.WriteFile(f, []byte(`
$primary: #333;
.nav { color: $primary; }
`), 0644))

	result, err := a.Analyze(context.Background(), f)
	require.NoError(t, err)
	assert.Equal(t, "css", result.Language)
	assert.Greater(t, len(result.Symbols), 0)
}
```

**Step 2: Run test to verify it fails**

```bash
cd /home/razvan/go/src/github.com/doITmagic/rag-code-mcp
go test ./pkg/parser/css/... -v
```
Expected: `cannot find package`

**Step 3: Write implementation**

Create `pkg/parser/css/analyzer.go`:

```go
package css

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
)

func init() {
	parser.Register(NewAnalyzer())
}

// Analyzer handles CSS and SCSS files.
type Analyzer struct {
	ts *docs.TreeSitterParser
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{ts: docs.NewTreeSitterParser()}
}

func (a *Analyzer) Name() string { return "css" }

func (a *Analyzer) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".css" || ext == ".scss"
}

func (a *Analyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("css: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return &parser.Result{Language: "css"}, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	symbols, err := a.ts.Parse(content, path, ext)
	if err != nil {
		return nil, fmt.Errorf("css: parse %s: %w", path, err)
	}
	// Override language on all symbols
	for i := range symbols {
		symbols[i].Language = "css"
	}
	return &parser.Result{Symbols: symbols, Language: "css"}, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/parser/css/... -v
```
Expected: all tests PASS

**Step 5: Commit**

```bash
git add pkg/parser/css/
git commit -m "feat(parser): add dedicated CSS/SCSS parser"
```

---

## Task 2: Create `pkg/parser/sql/analyzer.go`

**Files:**
- Create: `pkg/parser/sql/analyzer.go`
- Create: `pkg/parser/sql/analyzer_test.go`

**Step 1: Write the failing test**

Create `pkg/parser/sql/analyzer_test.go`:

```go
package sql_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_CanHandle(t *testing.T) {
	a := sql.NewAnalyzer()
	assert.True(t, a.CanHandle("schema.sql"))
	assert.True(t, a.CanHandle("MIGRATION.SQL"))
	assert.False(t, a.CanHandle("style.css"))
	assert.False(t, a.CanHandle("script.sh"))
}

func TestAnalyzer_Name(t *testing.T) {
	assert.Equal(t, "sql", sql.NewAnalyzer().Name())
}

func TestAnalyzer_ParseSQL(t *testing.T) {
	a := sql.NewAnalyzer()
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "schema.sql")
	require.NoError(t, os.WriteFile(f, []byte(`
CREATE TABLE users (
  id SERIAL PRIMARY KEY,
  email TEXT NOT NULL
);
`), 0644))

	result, err := a.Analyze(context.Background(), f)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "sql", result.Language)
	assert.Greater(t, len(result.Symbols), 0)
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/parser/sql/... -v
```
Expected: `cannot find package`

**Step 3: Write implementation**

Create `pkg/parser/sql/analyzer.go`:

```go
package sql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
)

func init() {
	parser.Register(NewAnalyzer())
}

// Analyzer handles SQL files.
type Analyzer struct {
	ts *docs.TreeSitterParser
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{ts: docs.NewTreeSitterParser()}
}

func (a *Analyzer) Name() string { return "sql" }

func (a *Analyzer) CanHandle(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".sql"
}

func (a *Analyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sql: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return &parser.Result{Language: "sql"}, nil
	}
	symbols, err := a.ts.Parse(content, path, ".sql")
	if err != nil {
		return nil, fmt.Errorf("sql: parse %s: %w", path, err)
	}
	for i := range symbols {
		symbols[i].Language = "sql"
	}
	return &parser.Result{Symbols: symbols, Language: "sql"}, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/parser/sql/... -v
```
Expected: all tests PASS

**Step 5: Commit**

```bash
git add pkg/parser/sql/
git commit -m "feat(parser): add dedicated SQL parser"
```

---

## Task 3: Create `pkg/parser/shell/analyzer.go`

**Files:**
- Create: `pkg/parser/shell/analyzer.go`
- Create: `pkg/parser/shell/analyzer_test.go`

**Step 1: Write the failing test**

Create `pkg/parser/shell/analyzer_test.go`:

```go
package shell_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/shell"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_CanHandle(t *testing.T) {
	a := shell.NewAnalyzer()
	assert.True(t, a.CanHandle("deploy.sh"))
	assert.True(t, a.CanHandle("run.bash"))
	assert.False(t, a.CanHandle("style.css"))
	assert.False(t, a.CanHandle("main.go"))
}

func TestAnalyzer_Name(t *testing.T) {
	assert.Equal(t, "shell", shell.NewAnalyzer().Name())
}

func TestAnalyzer_ParseShell(t *testing.T) {
	a := shell.NewAnalyzer()
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "deploy.sh")
	require.NoError(t, os.WriteFile(f, []byte(`
#!/bin/bash
function deploy() {
  echo "Deploying..."
}
deploy
`), 0644))

	result, err := a.Analyze(context.Background(), f)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "shell", result.Language)
	assert.Greater(t, len(result.Symbols), 0)
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/parser/shell/... -v
```
Expected: `cannot find package`

**Step 3: Write implementation**

Create `pkg/parser/shell/analyzer.go`:

```go
package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
)

func init() {
	parser.Register(NewAnalyzer())
}

// Analyzer handles Shell/Bash script files.
type Analyzer struct {
	ts *docs.TreeSitterParser
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{ts: docs.NewTreeSitterParser()}
}

func (a *Analyzer) Name() string { return "shell" }

func (a *Analyzer) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".sh" || ext == ".bash"
}

func (a *Analyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("shell: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return &parser.Result{Language: "shell"}, nil
	}
	symbols, err := a.ts.Parse(content, path, ".sh")
	if err != nil {
		return nil, fmt.Errorf("shell: parse %s: %w", path, err)
	}
	for i := range symbols {
		symbols[i].Language = "shell"
	}
	return &parser.Result{Symbols: symbols, Language: "shell"}, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/parser/shell/... -v
```
Expected: all tests PASS

**Step 5: Commit**

```bash
git add pkg/parser/shell/
git commit -m "feat(parser): add dedicated Shell/Bash parser"
```

---

## Task 4: Create `pkg/parser/svelte/analyzer.go`

**Files:**
- Create: `pkg/parser/svelte/analyzer.go`
- Create: `pkg/parser/svelte/analyzer_test.go`

**Step 1: Write the failing test**

Create `pkg/parser/svelte/analyzer_test.go`:

```go
package svelte_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/svelte"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_CanHandle(t *testing.T) {
	a := svelte.NewAnalyzer()
	assert.True(t, a.CanHandle("App.svelte"))
	assert.True(t, a.CanHandle("Button.SVELTE"))
	assert.False(t, a.CanHandle("App.vue"))
	assert.False(t, a.CanHandle("main.js"))
}

func TestAnalyzer_Name(t *testing.T) {
	assert.Equal(t, "svelte", svelte.NewAnalyzer().Name())
}

func TestAnalyzer_ParseSvelte(t *testing.T) {
	a := svelte.NewAnalyzer()
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "App.svelte")
	require.NoError(t, os.WriteFile(f, []byte(`
<script>
  let count = 0;
  function increment() { count++; }
</script>
<button on:click={increment}>{count}</button>
`), 0644))

	result, err := a.Analyze(context.Background(), f)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "svelte", result.Language)
	assert.Greater(t, len(result.Symbols), 0)
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/parser/svelte/... -v
```
Expected: `cannot find package`

**Step 3: Write implementation**

Create `pkg/parser/svelte/analyzer.go`:

```go
package svelte

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
)

func init() {
	parser.Register(NewAnalyzer())
}

// Analyzer handles Svelte Single File Components.
type Analyzer struct {
	ts *docs.TreeSitterParser
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{ts: docs.NewTreeSitterParser()}
}

func (a *Analyzer) Name() string { return "svelte" }

func (a *Analyzer) CanHandle(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".svelte"
}

func (a *Analyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("svelte: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return &parser.Result{Language: "svelte"}, nil
	}
	symbols, err := a.ts.Parse(content, path, ".svelte")
	if err != nil {
		return nil, fmt.Errorf("svelte: parse %s: %w", path, err)
	}
	for i := range symbols {
		symbols[i].Language = "svelte"
	}
	return &parser.Result{Symbols: symbols, Language: "svelte"}, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/parser/svelte/... -v
```
Expected: all tests PASS

**Step 5: Commit**

```bash
git add pkg/parser/svelte/
git commit -m "feat(parser): add dedicated Svelte parser"
```

---

## Task 5: Update `docs` analyzer — remove extracted extensions

**Files:**
- Modify: `pkg/parser/docs/analyzer.go` lines 33-45 and 57-64
- Modify: `pkg/parser/docs/analyzer_test.go` line 16-17

**Step 1: Update `CanHandle` in `pkg/parser/docs/analyzer.go`**

Change line 40 from:
```go
case ".yaml", ".yml", ".json", ".xml", ".toml", ".rst", ".css", ".scss", ".sql", ".sh", ".svelte":
```
To:
```go
case ".yaml", ".yml", ".json", ".xml", ".toml", ".rst":
```

**Step 2: Update `Analyze` comment in `pkg/parser/docs/analyzer.go`**

Change the comment on line 63 from:
```go
// Try treesitter for yaml, json, xml, toml, rst
```
To:
```go
// Try treesitter for yaml, json, xml, toml, rst (css/sql/shell/svelte have dedicated parsers)
```

**Step 3: Fix the test in `pkg/parser/docs/analyzer_test.go`**

Change `validExts` on line 16 to remove `style.css`, `main.scss`, `query.sql`, `script.sh`:
```go
validExts := []string{"test.md", "README.markdown", "config.yaml", "data.json", "conf.toml", "index.xml", "doc.rst"}
```

And remove the `TestAnalyzer_TreesitterParsing_CSS` test entirely (lines 129-150) since CSS is no longer handled by docs.

**Step 4: Run all docs tests**

```bash
go test ./pkg/parser/docs/... -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add pkg/parser/docs/
git commit -m "feat(parser): remove css/scss/sql/sh/svelte from docs parser"
```

---

## Task 6: Register new parsers in daemon

**Files:**
- Modify: `internal/daemon/run.go` (blank import block)

**Step 1: Add 4 blank imports to `run.go`**

Locate the existing blank import block in `internal/daemon/run.go` and add:
```go
_ "github.com/doITmagic/rag-code-mcp/pkg/parser/css"
_ "github.com/doITmagic/rag-code-mcp/pkg/parser/sql"
_ "github.com/doITmagic/rag-code-mcp/pkg/parser/shell"
_ "github.com/doITmagic/rag-code-mcp/pkg/parser/svelte"
```

**Step 2: Verify build**

```bash
go build ./...
```
Expected: no errors

**Step 3: Run full test suite**

```bash
go test ./pkg/parser/... -v -count=1
```
Expected: all PASS including new parsers

**Step 4: Commit**

```bash
git add internal/daemon/run.go
git commit -m "feat(daemon): register css/sql/shell/svelte parsers"
```

---

## Task 7: Move Trello cards to Done

After all tests pass:
1. Move card #50 (CSS/SCSS) → Done
2. Move card #51 (SQL) → Done
3. Move card #52 (Shell/Bash) → Done
4. Move card #53 (Svelte) → Done
