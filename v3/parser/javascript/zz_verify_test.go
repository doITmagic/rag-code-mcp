package javascript

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// Independent verification probes (written by a reviewer, not the implementer).

func qn(syms []parser.Symbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, string(s.Kind)+":"+s.QualifiedName)
	}
	return out
}

func relQN(s parser.Symbol, kind parser.RelationKind, name string) (string, bool) {
	for _, r := range s.Relations {
		if r.Kind == kind && r.TargetName == name {
			return r.TargetQualifiedName, true
		}
	}
	return "", false
}

const probeJSX = `import Base, { helper as h, Widget } from '../core/base.js';
import * as svc from './services/index';
import cfg from './config.json';
const { readFile: rf } = require('fs/promises');
const { join } = require('path');
const store = require('../store');

/**
 * Doc for Panel.
 * @param {object} props
 */
export function Panel(props) {
  const local = () => h(props);
  local();
  svc.load();
  store.get();
  rf('x');
  join('a', 'b');
  cfg.x();
  return <section><Widget /><svc.Card /><span /></section>;
}

// Line comment doc
export class Holder extends Base {
  static make() { return new Holder(); }
  run() { this.other(); Holder.make(); instance.go(); }
  other() {}
}

const instance = new Holder();
export let counter = 0;
var legacy = 1;
function App() { return Panel(); }
export default App;
module.exports.legacy = legacy;
`

func TestProbeJSXAndImports(t *testing.T) {
	syms := analyze(t, "src/ui/panel/Panel.jsx", probeJSX)
	t.Logf("symbols: %v", qn(syms))

	panel := find(t, syms, "src/ui/panel/Panel.Panel")
	assert.Equal(t, parser.KindFunction, panel.Kind)
	assert.True(t, panel.Exported)
	assert.Equal(t, 12, panel.StartLine)
	assert.Equal(t, 21, panel.EndLine)
	assert.Equal(t, "Doc for Panel.\n@param {object} props", panel.Doc)
	assert.Equal(t, "export function Panel(props)", panel.Signature)
	assert.True(t, strings.HasPrefix(panel.Content, "export function Panel"))

	for _, tc := range []struct {
		kind parser.RelationKind
		name string
		want string
	}{
		{parser.RelCall, "h", "src/ui/core/base.helper"},             // named alias, relative ../ with .js ext stripped
		{parser.RelCall, "load", "src/ui/panel/services/index.load"}, // namespace member
		{parser.RelCall, "get", "src/ui/store.get"},                  // require namespace from nested dir
		{parser.RelCall, "rf", "fs/promises.readFile"},               // destructured require rename, bare
		{parser.RelCall, "join", "path.join"},                        // destructured require, bare
		{parser.RelCall, "x", "src/ui/panel/config.default.x"},       // default import of json
		{parser.RelUsesType, "Widget", "src/ui/core/base.Widget"},
		{parser.RelUsesType, "Card", "src/ui/panel/services/index.Card"},
	} {
		got, ok := relQN(panel, tc.kind, tc.name)
		require.True(t, ok, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}
	// local arrow inside the function is not top-level: must be "".
	got, ok := relQN(panel, parser.RelCall, "local")
	require.True(t, ok)
	assert.Equal(t, "", got, "local closure is not a top-level name")
	// lowercase JSX element is not a type use
	_, ok = relQN(panel, parser.RelUsesType, "span")
	assert.False(t, ok)

	holder := find(t, syms, "src/ui/panel/Panel.Holder")
	assert.Equal(t, parser.KindClass, holder.Kind)
	assert.Equal(t, "Line comment doc", holder.Doc)
	got, _ = relQN(holder, parser.RelExtends, "Base")
	assert.Equal(t, "src/ui/core/base.default", got)

	run := find(t, syms, "src/ui/panel/Panel.Holder.run")
	assert.Equal(t, "Holder", run.Owner)
	got, _ = relQN(run, parser.RelCall, "other")
	assert.Equal(t, "src/ui/panel/Panel.Holder.other", got)
	got, _ = relQN(run, parser.RelCall, "make")
	assert.Equal(t, "src/ui/panel/Panel.Holder.make", got, "static call on local class")
	got, _ = relQN(run, parser.RelCall, "go")
	assert.Equal(t, "", got, "receiver is a variable of unknown type: must not be guessed")

	make_ := find(t, syms, "src/ui/panel/Panel.Holder.make")
	got, _ = relQN(make_, parser.RelUsesType, "Holder")
	assert.Equal(t, "src/ui/panel/Panel.Holder", got)

	assert.Equal(t, parser.KindVariable, find(t, syms, "src/ui/panel/Panel.counter").Kind)
	assert.True(t, find(t, syms, "src/ui/panel/Panel.counter").Exported)
	legacy := find(t, syms, "src/ui/panel/Panel.legacy")
	assert.Equal(t, parser.KindVariable, legacy.Kind)
	assert.True(t, legacy.Exported, "module.exports.legacy = legacy")

	app := find(t, syms, "src/ui/panel/Panel.default")
	assert.True(t, app.Exported, "export default App;")

	// imports and the instance const are symbols only when declared here
	for _, s := range syms {
		assert.NotContains(t, []string{"Base", "h", "Widget", "svc", "cfg", "rf", "join", "store"}, s.Name, "import must not be a symbol")
	}
}

func TestProbeTSEnumsAndGenerics(t *testing.T) {
	src := `import { Repo } from './repo';
import DefaultThing from './thing';
export enum Mode { A = 1, B }
export type Handler<T> = (x: T) => void;
export interface Svc<T> extends Repo<T> {}
export class Impl<T> extends Repo<T> implements Svc<T> {
  constructor(private readonly r: Repo<T>) { super(); }
  protected go(): void { Mode.A; DefaultThing.run(); new DefaultThing(); }
}
export default function () {}
`
	syms := analyze(t, "lib/impl.ts", src)
	t.Logf("symbols: %v", qn(syms))
	assert.Equal(t, parser.KindClass, find(t, syms, "lib/impl.Mode").Kind)
	assert.Equal(t, parser.KindType, find(t, syms, "lib/impl.Handler").Kind)
	svc := find(t, syms, "lib/impl.Svc")
	got, _ := relQN(svc, parser.RelExtends, "Repo")
	assert.Equal(t, "lib/repo.Repo", got, "generics stripped")
	impl := find(t, syms, "lib/impl.Impl")
	got, _ = relQN(impl, parser.RelExtends, "Repo")
	assert.Equal(t, "lib/repo.Repo", got)
	got, _ = relQN(impl, parser.RelImplements, "Svc")
	assert.Equal(t, "lib/impl.Svc", got)
	ctor := find(t, syms, "lib/impl.Impl.constructor")
	assert.Equal(t, parser.KindMethod, ctor.Kind)
	g := find(t, syms, "lib/impl.Impl.go")
	assert.True(t, g.Exported, "protected is not private")
	got, _ = relQN(g, parser.RelCall, "run")
	assert.Equal(t, "lib/thing.default.run", got)
	got, _ = relQN(g, parser.RelUsesType, "DefaultThing")
	assert.Equal(t, "lib/thing.default", got)
	def := find(t, syms, "lib/impl.default")
	assert.Equal(t, parser.KindFunction, def.Kind)
	assert.True(t, def.Exported)
}

func TestProbeVueDefineComponentAndCRLF(t *testing.T) {
	src := "<template>\r\n  <p/>\r\n</template>\r\n<script lang=\"ts\">\r\nimport { defineComponent } from 'vue';\r\nimport svc from './svc';\r\nexport default defineComponent({\r\n  methods: {\r\n    hit(): void { svc.ping(); this.other(); },\r\n    other() {},\r\n  },\r\n});\r\n</script>\r\n"
	syms := analyze(t, "src/My-widget.vue", src)
	t.Logf("symbols: %v", qn(syms))
	hit := find(t, syms, "src/My-widget.MyWidget.hit")
	assert.Equal(t, parser.KindMethod, hit.Kind)
	assert.Equal(t, 9, hit.StartLine)
	got, _ := relQN(hit, parser.RelCall, "ping")
	assert.Equal(t, "src/svc.default.ping", got)
	got, _ = relQN(hit, parser.RelCall, "other")
	assert.Equal(t, "src/My-widget.MyWidget.other", got)
	tpl := find(t, syms, "src/My-widget.MyWidget")
	assert.Equal(t, parser.KindTemplate, tpl.Kind)
	assert.Equal(t, 1, tpl.StartLine)
	assert.Equal(t, 3, tpl.EndLine)
}

func TestProbeRobustness(t *testing.T) {
	bin := make([]byte, 4096)
	for i := range bin {
		bin[i] = byte(i*7 + 13)
	}
	big := bytes.Repeat([]byte("export function f"+strings.Repeat("(", 2000)+"\n"), 50)
	cases := map[string][]byte{
		"one.js":       []byte("{"),
		"one.ts":       []byte("<"),
		"bin.js":       bin,
		"bin.vue":      bin,
		"trunc.tsx":    []byte("export const A = () => <div><B x={"),
		"trunc.vue":    []byte("<template><div></template><script setup lang=\"ts\">const x = ref(</scr"),
		"noclose.vue":  []byte("<script>export function f() {}"),
		"deep.js":      big,
		"nul.js":       []byte("export function f() {}\x00\x00function g() { f(); }"),
		"badattr.vue":  []byte("<script lang=ts>export const a = 1</script><template><i/></template>"),
		"emptyroot.js": []byte("export function f() {}"),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			var syms []parser.Symbol
			var err error
			go func() {
				defer close(done)
				root := root
				if name == "emptyroot.js" {
					root = ""
				}
				syms, err = analyzer{}.Analyze(parser.File{Path: filepath.Join("/ws", name), Root: root, Source: src})
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("hang > 5s")
			}
			require.NoError(t, err)
			for _, s := range syms {
				assert.NotEmpty(t, s.QualifiedName)
				assert.LessOrEqual(t, s.StartLine, s.EndLine)
			}
			if name == "emptyroot.js" {
				assert.Equal(t, "emptyroot.f", syms[0].QualifiedName, "empty Root -> stem")
			}
		})
	}
}

// ---- second verification round ----

func TestProbe2NestedCJSAndKinds(t *testing.T) {
	src := `const { Model: M, util } = require('../../core/model.cjs');
const log = require('../log');
const pkg = require('@scope/pkg');
import type { Shape } from '../../geo/types';
import { type Only } from './only';

/** First. */
var fnVar = function () { log.info(); util(); };
let fnLet = (x) => M.build(x);
var plainVar = 1;
const PLAIN = 2;
let plainLet = 3;
export { fnVar as renamed };
export default fnLet;

class Repo {
  get size() { return 0; }
  static async *gen() { yield pkg.x(); }
}
const repo = new Repo();
function user() { repo.load(); Repo.gen(); new M(); }
module.exports = fnVar;
`
	syms := analyze(t, "src/a/b/c.js", src)
	t.Logf("symbols: %v", qn(syms))

	fnVar := find(t, syms, "src/a/b/c.fnVar")
	assert.Equal(t, parser.KindFunction, fnVar.Kind)
	assert.True(t, fnVar.Exported, "export { fnVar as renamed } / module.exports = fnVar")
	assert.Equal(t, "First.", fnVar.Doc)
	assert.Equal(t, 8, fnVar.StartLine)
	got, _ := relQN(fnVar, parser.RelCall, "info")
	assert.Equal(t, "src/a/log.info", got, "require namespace, ../ from nested dir")
	got, _ = relQN(fnVar, parser.RelCall, "util")
	assert.Equal(t, "src/core/model.util", got, "destructured require, ../../ with .cjs stripped")

	fnLet := find(t, syms, "src/a/b/c.default")
	assert.Equal(t, parser.KindFunction, fnLet.Kind)
	assert.True(t, fnLet.Exported, "export default fnLet;")
	got, _ = relQN(fnLet, parser.RelCall, "build")
	assert.Equal(t, "src/core/model.Model.build", got, "destructured require rename")

	assert.Equal(t, parser.KindVariable, find(t, syms, "src/a/b/c.plainVar").Kind)
	assert.Equal(t, parser.KindConstant, find(t, syms, "src/a/b/c.PLAIN").Kind)
	assert.Equal(t, parser.KindVariable, find(t, syms, "src/a/b/c.plainLet").Kind)
	assert.False(t, find(t, syms, "src/a/b/c.PLAIN").Exported)

	repo := find(t, syms, "src/a/b/c.Repo")
	assert.False(t, repo.Exported)
	size := find(t, syms, "src/a/b/c.Repo.size")
	assert.Equal(t, parser.KindMethod, size.Kind)
	assert.Equal(t, "Repo", size.Owner)
	gen := find(t, syms, "src/a/b/c.Repo.gen")
	got, _ = relQN(gen, parser.RelCall, "x")
	assert.Equal(t, "@scope/pkg.x", got, "bare scoped specifier as-is")

	user := find(t, syms, "src/a/b/c.user")
	got, _ = relQN(user, parser.RelCall, "load")
	assert.Equal(t, "", got, "repo is a variable: receiver type unknown, never guess")
	got, _ = relQN(user, parser.RelCall, "gen")
	assert.Equal(t, "src/a/b/c.Repo.gen", got)
	got, _ = relQN(user, parser.RelUsesType, "M")
	assert.Equal(t, "src/core/model.Model", got)

	for _, s := range syms {
		assert.NotContains(t, []string{"M", "util", "log", "pkg", "Shape", "Only", "renamed"}, s.Name, "imports/re-exports are not symbols")
	}
}

func TestProbe2ComponentObjectInJS(t *testing.T) {
	src := `import { defineComponent } from 'vue';
import svc from './svc';
export default defineComponent({
  computed: { total() { return svc.sum(); } },
  methods: { reset() { this.total; this.clear(); }, clear() {} },
  mounted() { this.reset(); },
});
`
	syms := analyze(t, "src/widgets/cart-view.js", src)
	t.Logf("symbols: %v", qn(syms))
	reset := find(t, syms, "src/widgets/cart-view.CartView.reset")
	assert.Equal(t, parser.KindMethod, reset.Kind)
	assert.Equal(t, "CartView", reset.Owner)
	got, _ := relQN(reset, parser.RelCall, "clear")
	assert.Equal(t, "src/widgets/cart-view.CartView.clear", got)
	total := find(t, syms, "src/widgets/cart-view.CartView.total")
	got, _ = relQN(total, parser.RelCall, "sum")
	assert.Equal(t, "src/widgets/svc.default.sum", got)
	mounted := find(t, syms, "src/widgets/cart-view.CartView.mounted")
	got, _ = relQN(mounted, parser.RelCall, "reset")
	assert.Equal(t, "src/widgets/cart-view.CartView.reset", got)
}

func TestProbe2VueSetupTemplateFirst(t *testing.T) {
	src := `<template>
  <Card>
    <template #header>{{ title }}</template>
  </Card>
</template>

<script lang="ts" setup>
import { computed } from 'vue';
import * as api from '../api/index';
import type { Item } from '../api/types';

type Row = Item & { id: number };
enum State { On, Off }
const title = computed(() => api.title());
async function load(): Promise<Row[]> { return api.list(); }
</script>

<style scoped>.a{}</style>
`
	syms := analyze(t, "src/views/order-list.vue", src)
	t.Logf("symbols: %v", qn(syms))
	tpl := find(t, syms, "src/views/order-list.OrderList")
	assert.Equal(t, parser.KindTemplate, tpl.Kind)
	assert.Equal(t, 1, tpl.StartLine)
	assert.Equal(t, 5, tpl.EndLine)
	assert.True(t, strings.HasPrefix(tpl.Content, "<template>"))
	assert.True(t, strings.HasSuffix(tpl.Content, "</template>"))

	row := find(t, syms, "src/views/order-list.Row")
	assert.Equal(t, parser.KindType, row.Kind)
	assert.Equal(t, 12, row.StartLine)
	assert.Equal(t, parser.KindClass, find(t, syms, "src/views/order-list.State").Kind)
	title := find(t, syms, "src/views/order-list.title")
	assert.Equal(t, parser.KindConstant, title.Kind)
	assert.Equal(t, 14, title.StartLine)
	load := find(t, syms, "src/views/order-list.load")
	assert.Equal(t, parser.KindFunction, load.Kind)
	assert.Equal(t, 15, load.StartLine)
	assert.Equal(t, 15, load.EndLine)
	assert.Equal(t, "async function load(): Promise<Row[]>", load.Signature)
	got, _ := relQN(load, parser.RelCall, "list")
	assert.Equal(t, "src/api/index.list", got)
	for _, s := range syms {
		assert.NotEqual(t, parser.KindStyle, s.Kind)
	}
}

func TestProbe2Stateless(t *testing.T) {
	// Two different files analyzed back to back must not leak import tables.
	a := analyze(t, "x/a.js", "import { q } from './q';\nexport function fa() { q(); }\n")
	b := analyze(t, "x/b.js", "export function fb() { q(); }\n")
	got, _ := relQN(find(t, a, "x/a.fa"), parser.RelCall, "q")
	assert.Equal(t, "x/q.q", got)
	got, _ = relQN(find(t, b, "x/b.fb"), parser.RelCall, "q")
	assert.Equal(t, "", got)
}

// ---- third verification round ----

func TestProbe3CJSHandlersAndJSXInJS(t *testing.T) {
	src := `import Def, * as all from '../lib/all.mjs';
import { a as b, c } from './m';
import './side.css';
const q = require('./q').default;

exports.handler = async (req) => { b(); c(); all.run(); q.go(); return <Def.Thing /> };
module.exports.helper = function helper() { exports.handler(); };
function pub() {}
function hidden() {}
module.exports = { pub, alias: hidden };
export default class extends Def { go() { [1].map(() => this.step()); } step() {} #secret() {} }
`
	syms := analyze(t, "src/x/h.js", src)
	t.Logf("symbols: %v", qn(syms))
	h := find(t, syms, "src/x/h.handler")
	assert.Equal(t, parser.KindFunction, h.Kind)
	assert.True(t, h.Exported)
	assert.Equal(t, 6, h.StartLine)
	got, _ := relQN(h, parser.RelCall, "b")
	assert.Equal(t, "src/x/m.a", got, "alias maps to original name")
	got, _ = relQN(h, parser.RelCall, "c")
	assert.Equal(t, "src/x/m.c", got)
	got, _ = relQN(h, parser.RelCall, "run")
	assert.Equal(t, "src/lib/all.run", got, "mixed default+namespace import, .mjs stripped")
	got, _ = relQN(h, parser.RelUsesType, "Thing")
	assert.Equal(t, "src/lib/all.default.Thing", got)
	for _, r := range h.Relations {
		assert.Equal(t, 6, r.Line, "relation line = call line")
	}
	helper := find(t, syms, "src/x/h.helper")
	assert.True(t, helper.Exported)
	assert.Equal(t, parser.KindFunction, helper.Kind)
	assert.True(t, find(t, syms, "src/x/h.pub").Exported, "module.exports = { pub }")
	assert.True(t, find(t, syms, "src/x/h.hidden").Exported, "module.exports = { alias: hidden }")
	def := find(t, syms, "src/x/h.default")
	assert.Equal(t, parser.KindClass, def.Kind)
	got, _ = relQN(def, parser.RelExtends, "Def")
	assert.Equal(t, "src/lib/all.default", got)
	g := find(t, syms, "src/x/h.default.go")
	got, _ = relQN(g, parser.RelCall, "step")
	assert.Equal(t, "src/x/h.default.step", got, "this.m() inside arrow callback counts for the method")
	assert.False(t, find(t, syms, "src/x/h.default.#secret").Exported)
	for _, s := range syms {
		assert.NotContains(t, []string{"Def", "all", "b", "c"}, s.Name)
	}
}

func TestProbe3TSPrivateAndSyntaxErrorRecovery(t *testing.T) {
	src := `export class A {
  private hide(): void {}
  public show(): void { this.hide(); }
}
export function broken( {
export function after(): number { return 1; }
`
	syms := analyze(t, "t.ts", src)
	t.Logf("symbols: %v", qn(syms))
	assert.False(t, find(t, syms, "t.A.hide").Exported)
	assert.True(t, find(t, syms, "t.A.show").Exported)
	got, _ := relQN(find(t, syms, "t.A.show"), parser.RelCall, "hide")
	assert.Equal(t, "t.A.hide", got)
	assert.NotEmpty(t, syms, "syntax error must still return symbols")
}

func TestProbe3VueTwoScriptsSingleQuoteLang(t *testing.T) {
	src := "<script lang='ts'>\nexport default { name: 'x', methods: { m() { this.n(); }, n() {} } }\n</script>\n<script setup lang='ts'>\nconst count = 1;\nfunction inc(): void { count; }\n</script>\n<template><div/></template>\n"
	syms := analyze(t, "comp/two_scripts.vue", src)
	t.Logf("symbols: %v", qn(syms))
	m := find(t, syms, "comp/two_scripts.TwoScripts.m")
	assert.Equal(t, 2, m.StartLine)
	got, _ := relQN(m, parser.RelCall, "n")
	assert.Equal(t, "comp/two_scripts.TwoScripts.n", got)
	assert.Equal(t, 5, find(t, syms, "comp/two_scripts.count").StartLine)
	inc := find(t, syms, "comp/two_scripts.inc")
	assert.Equal(t, 6, inc.StartLine)
	assert.Equal(t, "function inc(): void", inc.Signature)
	tpl := find(t, syms, "comp/two_scripts.TwoScripts")
	assert.Equal(t, 8, tpl.StartLine)

	// .vue with neither script nor template: no symbols, no error, no panic
	syms, err := analyzer{}.Analyze(parser.File{Path: "/ws/empty.vue", Root: "/ws", Source: []byte("<style>.a{}</style>")})
	require.NoError(t, err)
	assert.Empty(t, syms)
}

func TestProbe3DeepNestingValidFile(t *testing.T) {
	// 300 nested arrays is valid JS; the unbalanced guard must not drop a parseable file silently
	// without at least returning cleanly.
	src := "export const deep = " + strings.Repeat("[", 300) + strings.Repeat("]", 300) + ";\nexport function f() {}\n"
	syms := analyze(t, "deep.js", src)
	t.Logf("deep symbols: %v", qn(syms))
}

func TestProbe3Concurrent8(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				src := probeJSX
				if i%2 == 0 {
					src = "import { x } from './x';\nexport function f() { x(); }\n"
				}
				_, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(root, "c", "f.jsx"), Root: root, Source: []byte(src)})
				assert.NoError(t, err)
			}
		}(i)
	}
	wg.Wait()
}
