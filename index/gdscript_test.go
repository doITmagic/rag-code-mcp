package index

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/gdscript"
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

// super.run() in a class_name hierarchy names <Base>.run with the short class
// name; run is defined two levels up, in the base's own base, which is
// declared by name only (extends Cutscene).
func TestResolveGDScriptSuperThroughClassNameBases(t *testing.T) {
	e := newEnv(t)
	e.write(t, "project.godot", "")
	cut := e.write(t, "cutscene.gd", "class_name Cutscene extends Node\n\nfunc run():\n\tpass\n")
	e.write(t, "trigger.gd", "class_name BaseTrigger extends Cutscene\n\nfunc _ready():\n\tpass\n")
	src := "class_name Pickup extends BaseTrigger\n\nfunc _go():\n\tsuper.run()\n"
	pick := e.write(t, "pickup.gd", src)
	check := func(t *testing.T) {
		t.Helper()
		rels := e.symbols(t, pick)["_go"].Relations
		require.Len(t, rels, 1)
		require.Equal(t, "BaseTrigger.run", rels[0].TargetQualifiedName)
		assert.Equal(t, e.symbols(t, cut)["run"].ID, rels[0].TargetID)
		assert.Equal(t, parser.ResolutionExact, rels[0].Resolution)
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	t.Run("full run", check)

	// Only pickup.gd is re-parsed: BaseTrigger is in the store, whose bases
	// are not followed, so run is matched by name.
	e.write(t, "pickup.gd", src+"\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{pick} })
	require.NoError(t, err)
	t.Run("files run", func(t *testing.T) {
		r := e.symbols(t, pick)["_go"].Relations[0]
		assert.Equal(t, e.symbols(t, cut)["run"].ID, r.TargetID)
		assert.Equal(t, parser.ResolutionUnique, r.Resolution)
	})
}

// A scene node's attached script (script = ExtResource(...)) is a use of the
// script's class, as extends "res://x.gd" is.
func TestResolveGDScriptSceneScript(t *testing.T) {
	e := newEnv(t)
	e.write(t, "project.godot", "")
	plain := e.write(t, "actors/plain.gd", "extends Node\n\nfunc a():\n\tpass\n")
	scene := e.write(t, "main.tscn", "[gd_scene load_steps=2 format=3]\n\n[ext_resource type=\"Script\" path=\"res://actors/plain.gd\" id=\"1_a\"]\n\n[node name=\"Map\" type=\"Node2D\"]\nscript = ExtResource(\"1_a\")\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	var got []string
	for _, s := range e.symbols(t, scene) {
		for _, r := range s.Relations {
			if r.Kind == parser.RelUsesType {
				got = append(got, r.TargetID)
			}
		}
	}
	assert.Equal(t, []string{e.symbols(t, plain)["plain"].ID}, got)
}
