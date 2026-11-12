package javascript

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// Components and handlers used in a Vue <template> were no usages.
func TestVueTemplateUsages(t *testing.T) {
	src := `<template>
  <div>
    <VersionSelect v-model="ver" @change="setVersion(ver)" />
    <status-card :label="format(x)" @click="toggle" v-if="isOn()"></status-card>
    <button @click="counter.increment()">{{ label(n) }}</button>
    <router-view />
  </div>
</template>

<script setup>
import VersionSelect from './VersionSelect.vue'
import StatusCard from '../cards/status-card.vue'
import { format } from '../lib/fmt'
const counter = useCounter()
function setVersion(v) {}
function toggle() {}
function isOn() { return true }
</script>
`
	syms := analyze(t, "src/comp/Header.vue", src)
	tpl := find(t, syms, "src/comp/Header.Header")
	assert.Equal(t, parser.Relation{Kind: parser.RelUsesType, TargetName: "VersionSelect", TargetQualifiedName: "src/comp/VersionSelect.default", Line: 3}, rel(t, tpl, parser.RelUsesType, "VersionSelect"))
	assert.Equal(t, parser.Relation{Kind: parser.RelUsesType, TargetName: "StatusCard", TargetQualifiedName: "src/cards/status-card.default", Line: 4}, rel(t, tpl, parser.RelUsesType, "StatusCard"))
	assert.Equal(t, "src/comp/Header.setVersion", rel(t, tpl, parser.RelCall, "setVersion").TargetQualifiedName)
	assert.Equal(t, 3, rel(t, tpl, parser.RelCall, "setVersion").Line)
	assert.Equal(t, "src/lib/fmt.format", rel(t, tpl, parser.RelCall, "format").TargetQualifiedName)
	assert.Equal(t, "src/comp/Header.toggle", rel(t, tpl, parser.RelCall, "toggle").TargetQualifiedName, "a bare handler name is a call")
	assert.Equal(t, "src/comp/Header.isOn", rel(t, tpl, parser.RelCall, "isOn").TargetQualifiedName)
	assert.Equal(t, "", rel(t, tpl, parser.RelCall, "increment").TargetQualifiedName, "a variable's type is never guessed")
	assert.Equal(t, "", rel(t, tpl, parser.RelCall, "label").TargetQualifiedName)
	assert.Equal(t, 5, rel(t, tpl, parser.RelCall, "label").Line)
	assert.Equal(t, "", rel(t, tpl, parser.RelUsesType, "RouterView").TargetQualifiedName, "a global component: by name")
	for _, r := range tpl.Relations {
		assert.NotEqual(t, "div", r.TargetName)
		assert.NotEqual(t, "ver", r.TargetName, "v-model binds, it does not call")
	}
}

// An options-API component's methods are what its template handlers call.
func TestVueTemplateOptionsAPIHandler(t *testing.T) {
	src := `<template><button @click="go" v-on:keyup.enter="stop()">x</button></template>
<script>
export default { methods: { go() {}, stop() {} } }
</script>
`
	syms := analyze(t, "src/todo-item.vue", src)
	tpl := find(t, syms, "src/todo-item.TodoItem")
	assert.Equal(t, "src/todo-item.TodoItem.go", rel(t, tpl, parser.RelCall, "go").TargetQualifiedName)
	assert.Equal(t, "src/todo-item.TodoItem.stop", rel(t, tpl, parser.RelCall, "stop").TargetQualifiedName)
}

// A component or call inside an HTML comment of the template is not used.
func TestVueTemplateCommentNoUsage(t *testing.T) {
	src := `<template>
  <div>
    <!-- <LegacyCard :item="pick(x)" /> -->
    <span>{{ fmt(x) }}</span>
  </div>
</template>

<script setup>
import LegacyCard from './LegacyCard.vue'
function pick(v) { return v }
function fmt(v) { return v }
</script>
`
	syms := analyze(t, "src/comp/Box.vue", src)
	tpl := find(t, syms, "src/comp/Box.Box")
	for _, r := range tpl.Relations {
		assert.NotEqual(t, "LegacyCard", r.TargetName, "commented-out tag")
		assert.NotEqual(t, "pick", r.TargetName, "commented-out binding")
	}
	assert.Equal(t, 4, rel(t, tpl, parser.RelCall, "fmt").Line, "lines kept after the comment")
}

// A call on a later line of a multi-line attribute value is on its own line.
func TestVueTemplateMultilineCallLine(t *testing.T) {
	src := `<template>
  <button
    @click="
      save()
    "
    @keyup="
      stop
    ">x</button>
</template>

<script setup>
function save() {}
function stop() {}
</script>
`
	syms := analyze(t, "src/comp/Box.vue", src)
	tpl := find(t, syms, "src/comp/Box.Box")
	assert.Equal(t, 4, rel(t, tpl, parser.RelCall, "save").Line)
	assert.Equal(t, 7, rel(t, tpl, parser.RelCall, "stop").Line, "a bare handler name on its own line")
}

// Words inside a string literal of a template expression are no calls.
func TestVueTemplateStringNoCall(t *testing.T) {
	src := `<template>
  <button @click="ask('Delete the item(s)?')" :title="` + "`x ${y}(z)`" + `">{{ $t('Save (now)') }}{{ fmt("a (b)") }}</button>
</template>

<script setup>
function ask() {}
function fmt() {}
</script>
`
	syms := analyze(t, "src/comp/Box.vue", src)
	tpl := find(t, syms, "src/comp/Box.Box")
	var got []string
	for _, r := range tpl.Relations {
		if r.Kind == parser.RelCall {
			got = append(got, r.TargetName)
		}
	}
	assert.Equal(t, []string{"ask", "$t", "fmt"}, got)
}

// <widgets.Button /> of `import * as widgets` is a component usage whatever
// the namespace's case; a lowercase dotted tag of no import, or one inside an
// HTML comment, is not.
func TestVueTemplateNamespaceComponent(t *testing.T) {
	syms := analyze(t, "Page.vue", `<template><widgets.Button /><other.Thing /><!-- <widgets.Card /> --></template>
<script setup>
import * as widgets from './widgets';
</script>
`)
	tpl := find(t, syms, "Page.Page")
	var used []string
	for _, r := range tpl.Relations {
		if r.Kind == parser.RelUsesType {
			used = append(used, r.TargetQualifiedName)
		}
	}
	if len(used) != 1 || used[0] != "widgets.Button" {
		t.Fatalf("component usages = %q; want only widgets.Button", used)
	}
}
