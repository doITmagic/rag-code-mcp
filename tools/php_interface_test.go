package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const phpRepoCaller = `<?php
namespace App\Http;
use App\Repositories\UserRepositoryInterface;
class UserController {
    public function __construct(private UserRepositoryInterface $repo) {}
    public function show() { return $this->repo->find(1); }
}
function list_users(UserRepositoryInterface $repo) { return $repo->find(2); }
`

// viaOf maps each usage's caller to its via marker.
func viaOf(files []FileUsages) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, u := range f.Usages {
			out[u.Caller] = u.Via
		}
	}
	return out
}

// A call through an interface-typed variable is also a usage of the method
// of every workspace class implementing the interface (directly, through a
// parent class, through an interface extending it), marked via; the
// interface's own method keeps its usages.
func TestUsagesOfPhpMethodThroughInterface(t *testing.T) {
	v := newEnv(t, false, nil)
	impl := "app/Repositories/EloquentUserRepository.php"
	v.addFiles(t, map[string]string{
		"app/Repositories/UserRepositoryInterface.php": "<?php\nnamespace App\\Repositories;\ninterface UserRepositoryInterface { public function find($id); }\ninterface ReadRepository extends UserRepositoryInterface {}\n",
		impl:                          "<?php\nnamespace App\\Repositories;\nclass EloquentUserRepository implements UserRepositoryInterface { public function find($id) { return $id; } }\n",
		"app/Repositories/Cached.php": "<?php\nnamespace App\\Repositories;\nabstract class BaseRepository implements UserRepositoryInterface {}\nclass CachedUserRepository extends BaseRepository { public function find($id) { return $id; } }\nclass ApiUserRepository implements ReadRepository { public function find($id) { return $id; } }\nclass Unrelated { public function find($id) { return $id; } }\n",
		"app/Http/UserController.php": phpRepoCaller,
	})
	want := map[string]string{
		`App\Http.UserController.show`: `App\Repositories.UserRepositoryInterface`,
		`App\Http.list_users`:          `App\Repositories.UserRepositoryInterface`,
	}
	check := func() {
		t.Helper()
		for _, s := range []string{`App\Repositories\EloquentUserRepository::find`, `App\Repositories\CachedUserRepository::find`, `App\Repositories\ApiUserRepository::find`} {
			assert.Equal(t, want, viaOf(usagesOf(t, v, s)), s)
		}
		assert.Equal(t, map[string]string{`App\Http.UserController.show`: "", `App\Http.list_users`: ""},
			viaOf(usagesOf(t, v, `App\Repositories\UserRepositoryInterface::find`)))
		assert.Empty(t, usagesOf(t, v, `App\Repositories\Unrelated::find`))

		var out struct {
			Incoming *Node `json:"incoming"`
		}
		r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": `App\Repositories\EloquentUserRepository::find`, "depth": 1})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		got := map[string]string{}
		for _, c := range out.Incoming.Children {
			got[c.QualifiedName] = c.Via
		}
		assert.Equal(t, want, got)
	}
	check()

	// A files run of the implementing class alone gives the same answer.
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	p := filepath.Join(v.root, impl)
	require.NoError(t, os.WriteFile(p, []byte("<?php\nnamespace App\\Repositories;\n\nclass EloquentUserRepository implements UserRepositoryInterface { public function find($id) { return $id + 1; } }\n"), 0o644))
	require.NoError(t, os.Chtimes(p, time.Now(), time.Now().Add(time.Second)))
	w.IndexFiles([]string{p}, nil)
	require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
	check()
}

// limit counts the direct and the via callers together: a cut through an
// interface's callers says to raise limit, never that files were deleted.
func TestUsagesThroughInterfaceRespectLimit(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/All.php": "<?php\nnamespace App;\ninterface Finder { public function find(); }\nclass Repo implements Finder { public function find() {} }\n" +
			"class A { public function a(Finder $f) { $f->find(); } }\nclass B { public function b(Finder $f) { $f->find(); } }\n" +
			"class C { public function c(Repo $r) { $r->find(); } }\n",
	})
	for _, limit := range []int{1, 2} {
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": `App\Repo::find`, "limit": limit})
		require.Equal(t, "ok", r.Status, r.Message)
		var out struct {
			Total     int  `json:"total"`
			Truncated bool `json:"truncated"`
		}
		data(t, r, &out)
		assert.Equal(t, limit, out.Total, r.Message)
		assert.True(t, out.Truncated)
		assert.Contains(t, r.Message, fmt.Sprintf("only the first %d referencing symbols: raise limit", limit))
		assert.NotContains(t, r.Message, "deleted from disk")
	}
}
