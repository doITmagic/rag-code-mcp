package tools

import (
	"path/filepath"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Vue component used by tag with no import (Nuxt auto-import, global
// registration) was no usage of it: rag_find_usages listed only the importer.
func TestUsagesOfAutoImportedVueComponent(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"AvatarCard.vue":     "<script setup>\nconst props = defineProps(['name'])\n</script>\n<template><b>{{ props.name }}</b></template>\n",
		"AutoParent.vue":     "<template><AvatarCard /></template>\n",
		"ExplicitParent.vue": "<script setup>\nimport AvatarCard from './AvatarCard.vue'\n</script>\n<template><AvatarCard /></template>\n",
		"Tree.vue":           "<script setup>\ndefineProps(['items'])\n</script>\n<template><Tree v-for=\"i in items\" :items=\"i.children\" /></template>\n",
	})

	var out struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "AvatarCard", "file_path": "AvatarCard.vue"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	got := map[string]bool{}
	for _, f := range out.Files {
		for _, u := range f.Usages {
			got[filepath.Base(f.FilePath)] = u.ByName
		}
	}
	assert.Equal(t, map[string]bool{"AutoParent.vue": true, "ExplicitParent.vue": false}, got, r.Message)

	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Tree", "file_path": "Tree.vue"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, 1, out.Total, "a recursive component uses itself: %s", r.Message)
}
