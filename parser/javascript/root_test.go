package javascript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// symlink makes link point to target, skipping the test where the OS refuses
// (Windows without the symlink privilege).
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
}

// callTarget analyzes file (root-relative) in ws and returns the
// TargetQualifiedName of the single relation of symbol fn.
func callTarget(t *testing.T, ws, file, src, fn string) string {
	t.Helper()
	p := filepath.Join(ws, file)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(src), 0o644))
	syms, err := analyzer{}.Analyze(parser.File{Path: p, Root: ws, Source: []byte(src), Language: "javascript"})
	require.NoError(t, err)
	run := find(t, syms, fn)
	require.Len(t, run.Relations, 1)
	return run.Relations[0].TargetQualifiedName
}

// A re-exporting module or a tsconfig reached through `..` or a symlinked
// directory that leads out of the workspace root is treated as absent: no
// name written there reaches the index. Symlinks and parent-directory
// "extends" that stay inside the root keep working.
func TestProjectFilesOutsideRootNotRead(t *testing.T) {
	setup := func(t *testing.T) (ws, outside string) {
		base := t.TempDir()
		ws, outside = filepath.Join(base, "ws"), filepath.Join(base, "outside")
		require.NoError(t, os.MkdirAll(ws, 0o755))
		require.NoError(t, os.MkdirAll(outside, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "barrel.ts"), []byte("export { hiddenVaultKey as pub } from './private_vault';\n"), 0o644))
		return ws, outside
	}
	call := "export function run() { pub(); }\n"

	t.Run("barrel through ..", func(t *testing.T) {
		ws, _ := setup(t)
		got := callTarget(t, ws, "src/app.ts", "import { pub } from '../../outside/barrel';\n"+call, "src/app.run")
		assert.Equal(t, "../outside/barrel.pub", got)
	})

	t.Run("barrel through a symlinked directory", func(t *testing.T) {
		ws, outside := setup(t)
		symlink(t, outside, filepath.Join(ws, "linked"))
		got := callTarget(t, ws, "app.ts", "import { pub } from './linked/barrel';\n"+call, "app.run")
		assert.Equal(t, "linked/barrel.pub", got)
	})

	t.Run("barrel through a symlink inside the root", func(t *testing.T) {
		ws, _ := setup(t)
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "lib"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "lib", "barrel.ts"), []byte("export * from './impl';\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "lib", "impl.ts"), []byte("export function pub() {}\n"), 0o644))
		symlink(t, filepath.Join(ws, "lib"), filepath.Join(ws, "linked"))
		got := callTarget(t, ws, "app.ts", "import { pub } from './linked/barrel';\n"+call, "app.run")
		assert.Equal(t, "linked/impl.pub", got)
	})

	t.Run("tsconfig extends through a symlinked directory", func(t *testing.T) {
		ws, outside := setup(t)
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "real"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "real", "auth.ts"), []byte("export function pub() {}\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "base.json"), []byte(`{"compilerOptions":{"baseUrl":"..","paths":{"@outside-secret/*":["./real/*"]}}}`), 0o644))
		symlink(t, outside, filepath.Join(ws, "linked"))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "tsconfig.json"), []byte(`{"extends":"./linked/base.json"}`), 0o644))
		got := callTarget(t, ws, "app.ts", "import { pub } from '@outside-secret/auth';\n"+call, "app.run")
		assert.Equal(t, "@outside-secret/auth.pub", got)
	})

	t.Run("tsconfig extends a parent directory inside the root", func(t *testing.T) {
		ws, _ := setup(t)
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "real"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "real", "auth.ts"), []byte("export function pub() {}\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "tsconfig.base.json"), []byte(`{"compilerOptions":{"paths":{"@/*":["./*"]}}}`), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "packages", "web"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "packages", "web", "tsconfig.json"), []byte(`{"extends":"../../tsconfig.base.json"}`), 0o644))
		got := callTarget(t, ws, "packages/web/app.ts", "import { pub } from '@/real/auth';\n"+call, "packages/web/app.run")
		assert.Equal(t, "real/auth.pub", got)
	})
}
