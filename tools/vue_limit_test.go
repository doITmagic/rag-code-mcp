package tools

import (
	"path/filepath"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func r3oDump(n *Node, depth int, out *[]string) {
	for _, c := range n.Children {
		*out = append(*out, filepath.Base(c.FilePath)+"|"+c.QualifiedName+"|"+string(rune('0'+depth)))
		r3oDump(c, depth+1, out)
	}
}

func TestR3oVueHierarchyDepth(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"AvatarCard.vue":     "<script setup>\nconst props = defineProps(['name'])\n</script>\n<template><b>{{ props.name }}</b></template>\n",
		"AutoParent.vue":     "<template><AvatarCard /></template>\n",
		"ExplicitParent.vue": "<script setup>\nimport AvatarCard from './AvatarCard.vue'\n</script>\n<template><AvatarCard /><AvatarCard /></template>\n",
		"Grand.vue":          "<template><AutoParent /><ExplicitParent /></template>\n",
		"Grand2.vue":         "<script setup>\nimport ExplicitParent from './ExplicitParent.vue'\n</script>\n<template><ExplicitParent /></template>\n",
		"Tree.vue":           "<script setup>\ndefineProps(['items'])\n</script>\n<template><Tree v-for=\"i in items\" :items=\"i.children\" /></template>\n",
		"Forest.vue":         "<template><Tree /></template>\n",
	})
	for _, symbol := range []string{"AvatarCard", "Tree"} {
		var out struct {
			Incoming *Node  `json:"incoming"`
			Text     string `json:"text"`
		}
		r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": symbol, "direction": "incoming", "depth": 3})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		t.Logf("%s:\n%s", symbol, out.Text)
	}
	var out struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "ExplicitParent", "file_path": "ExplicitParent.vue"})
	data(t, r, &out)
	t.Logf("usages ExplicitParent: %s %+v", r.Message, out)
}

func TestR3oExportsMarkdownDir(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"docs/guide.md": "# Guide\n\nSome text about things.\n\n## Install\n\nRun it.\n",
	})
	r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "docs"})
	t.Logf("%s | %s", r.Status, r.Message)
	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "docs/guide.md"})
	t.Logf("%s | %s", r.Status, r.Message)
}

func TestVueTagParentPastLimitIsTruncated(t *testing.T) {
	v := newEnv(t, false, nil)
	files := map[string]string{
		"AvatarCard.vue": "<script setup>\nconst props = defineProps(['name'])\n</script>\n<template><b>{{ props.name }}</b></template>\n",
		"AutoParent.vue": "<template><AvatarCard /></template>\n",
	}
	for i := 0; i < candidateLimit; i++ {
		files["P"+string(rune('A'+i/26))+string(rune('a'+i%26))+".vue"] = "<script setup>\nimport AvatarCard from './AvatarCard.vue'\n</script>\n<template><AvatarCard /></template>\n"
	}
	v.addFiles(t, files)
	var u map[string]any
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "AvatarCard", "file_path": "AvatarCard.vue", "limit": candidateLimit})
	data(t, r, &u)
	var h map[string]any
	r2, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "AvatarCard", "direction": "incoming", "depth": 1})
	data(t, r2, &h)
	t.Logf("usages msg=%s truncated=%v total=%v", r.Message, u["truncated"], u["total"])
	assert.Equal(t, true, h["incoming_truncated"], "hierarchy drops AutoParent.vue silently")
	assert.Equal(t, true, u["truncated"], "find_usages drops AutoParent.vue silently: %s", r.Message)
}
