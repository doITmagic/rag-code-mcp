package index

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/gdscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extends "res://x.gd" names a script, not a symbol: it resolves to the
// script's class, whether declared by class_name or named after the file.
func TestResolveGDScriptPathExtends(t *testing.T) {
	e := newEnv(t)
	e.write(t, "project.godot", "")
	base := e.write(t, "actors/base.gd", "class_name BaseEnemy\nextends Node\n\nfunc hit():\n\tpass\n")
	plain := e.write(t, "actors/plain.gd", "extends Node\n\nfunc a():\n\tpass\n")
	orc := e.write(t, "actors/orc.gd", "extends \"res://actors/base.gd\"\n\nfunc b():\n\tpass\n")
	gob := e.write(t, "actors/gob.gd", "extends \"res://actors/plain.gd\"\n\nfunc c():\n\tpass\n")

	extends := func(t *testing.T, path, class string) parser.Relation {
		t.Helper()
		rels := e.symbols(t, path)[class].Relations
		require.Len(t, rels, 1)
		require.Equal(t, parser.RelExtends, rels[0].Kind)
		return rels[0]
	}
	check := func(t *testing.T) {
		assert.Equal(t, e.symbols(t, base)["BaseEnemy"].ID, extends(t, orc, "orc").TargetID)
		assert.Equal(t, e.symbols(t, plain)["plain"].ID, extends(t, gob, "gob").TargetID)
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	t.Run("full run", check)

	// Only the subclasses are re-parsed: the bases come from the store.
	e.write(t, "actors/orc.gd", "extends \"res://actors/base.gd\"\n\nfunc b2():\n\tpass\n")
	e.write(t, "actors/gob.gd", "extends \"res://actors/plain.gd\"\n\nfunc c2():\n\tpass\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{orc, gob} })
	require.NoError(t, err)
	t.Run("incremental run", check)
}
