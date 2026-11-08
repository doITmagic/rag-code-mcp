package javascript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const root = "/ws"

func analyze(t *testing.T, rel, src string) []parser.Symbol {
	t.Helper()
	syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(root, rel), Root: root, Source: []byte(src), Language: "javascript"})
	require.NoError(t, err)
	return syms
}

func find(t *testing.T, syms []parser.Symbol, qn string) parser.Symbol {
	t.Helper()
	for _, s := range syms {
		if s.QualifiedName == qn {
			return s
		}
	}
	names := make([]string, 0, len(syms))
	for _, s := range syms {
		names = append(names, s.QualifiedName)
	}
	require.Failf(t, "symbol not found", "%s not in %v", qn, names)
	return parser.Symbol{}
}

func rel(t *testing.T, s parser.Symbol, kind parser.RelationKind, name string) parser.Relation {
	t.Helper()
	for _, r := range s.Relations {
		if r.Kind == kind && r.TargetName == name {
			return r
		}
	}
	require.Failf(t, "relation not found", "%s %s not in %+v", kind, name, s.Relations)
	return parser.Relation{}
}

const esm = `import React, { useState as us, Foo } from 'react';
import * as ns from './util';
import helper from '../lib/helper';
const fs = require('fs');
const { a, b: c } = require('./cjs');

/** Formats x. */
export function f(x) {
  return ns.g(x) + us(1) + helper() + local() + a() + c() + fs.readFile(x);
}

/** Arrow doc */
export const arrow = async (y) => {
  [1].map(() => obj.m(y));
  new Foo();
};

function local() {}

export const K = 1;
let v = 2;
export default class A extends Foo {
  /** ctor */
  constructor() { super(); this.m(); }
  static m() { return f(); }
  async #priv() {}
}
module.exports = { local };
exports.z = function () { return v; };
`

func TestESMAndCJS(t *testing.T) {
	syms := analyze(t, "src/app/main.js", esm)

	f := find(t, syms, "src/app/main.f")
	assert.Equal(t, parser.KindFunction, f.Kind)
	assert.Equal(t, "src/app/main", f.Module)
	assert.Empty(t, f.Owner)
	assert.True(t, f.Exported)
	assert.Equal(t, "Formats x.", f.Doc)
	assert.Equal(t, "export function f(x)", f.Signature)
	assert.Equal(t, 8, f.StartLine)
	assert.Equal(t, 10, f.EndLine)
	for _, tc := range []struct{ name, qn string }{
		{"g", "src/app/util.g"},              // namespace import
		{"us", "react.useState"},             // named import with alias
		{"helper", "src/lib/helper.default"}, // default import, relative from nested dir
		{"local", "src/app/main.local"},      // top-level in this file
		{"a", "src/app/cjs.a"},               // destructured require
		{"c", "src/app/cjs.b"},               // destructured require with rename
		{"readFile", "fs.readFile"},          // require namespace
	} {
		r := rel(t, f, parser.RelCall, tc.name)
		assert.Equal(t, tc.qn, r.TargetQualifiedName, tc.name)
		assert.Equal(t, 9, r.Line, tc.name)
	}

	arrow := find(t, syms, "src/app/main.arrow")
	assert.Equal(t, parser.KindFunction, arrow.Kind)
	assert.True(t, arrow.Exported)
	assert.Equal(t, "Arrow doc", arrow.Doc)
	assert.Equal(t, "export const arrow = async (y) =>", arrow.Signature)
	assert.Equal(t, 13, arrow.StartLine)
	assert.Equal(t, 16, arrow.EndLine)
	assert.Equal(t, "", rel(t, arrow, parser.RelCall, "m").TargetQualifiedName, "unknown receiver")
	assert.Equal(t, 14, rel(t, arrow, parser.RelCall, "m").Line, "callback counts toward arrow")
	assert.Equal(t, "react.Foo", rel(t, arrow, parser.RelUsesType, "Foo").TargetQualifiedName)

	local := find(t, syms, "src/app/main.local")
	assert.True(t, local.Exported, "module.exports = { local }")
	assert.Equal(t, parser.KindConstant, find(t, syms, "src/app/main.K").Kind)
	v := find(t, syms, "src/app/main.v")
	assert.Equal(t, parser.KindVariable, v.Kind)
	assert.False(t, v.Exported)

	z := find(t, syms, "src/app/main.z")
	assert.Equal(t, parser.KindFunction, z.Kind)
	assert.True(t, z.Exported)
	assert.Equal(t, 29, z.StartLine)

	// imports must not become symbols
	for _, s := range syms {
		assert.NotEqual(t, "fs", s.Name)
		assert.NotEqual(t, "a", s.Name)
	}
}

func TestClassWithMethods(t *testing.T) {
	syms := analyze(t, "src/app/main.js", esm)

	cls := find(t, syms, "src/app/main.default")
	assert.Equal(t, parser.KindClass, cls.Kind)
	assert.True(t, cls.Exported)
	assert.Equal(t, 22, cls.StartLine)
	assert.Equal(t, 27, cls.EndLine)
	assert.Equal(t, "export default class A extends Foo", cls.Signature)
	assert.Equal(t, "react.Foo", rel(t, cls, parser.RelExtends, "Foo").TargetQualifiedName)

	ctor := find(t, syms, "src/app/main.default.constructor")
	assert.Equal(t, parser.KindMethod, ctor.Kind)
	assert.Equal(t, "A", ctor.Owner)
	assert.Equal(t, "ctor", ctor.Doc)
	assert.Equal(t, 24, ctor.StartLine)
	assert.Equal(t, "src/app/main.default.m", rel(t, ctor, parser.RelCall, "m").TargetQualifiedName)

	m := find(t, syms, "src/app/main.default.m")
	assert.Equal(t, "static m()", m.Signature)
	assert.True(t, m.Exported)
	assert.Equal(t, "src/app/main.f", rel(t, m, parser.RelCall, "f").TargetQualifiedName)

	priv := find(t, syms, "src/app/main.default.#priv")
	assert.False(t, priv.Exported)
	assert.Equal(t, "async #priv()", priv.Signature)
}

func TestTypeScript(t *testing.T) {
	src := `import type { Base } from './base';
import { type Shape, Point } from '../geo/shapes';

/** An interface. */
export interface I extends Base, Shape {
  a: number;
}
export type Pair<T> = [T, T];
export enum Color { Red, Green }
export abstract class C extends Base implements I {
  private secret(): void { this.run(); }
  public run = (): Point => new Point();
  abstract build(): void;
}
export declare function dec(): void;
const hidden = 1;
export { hidden as shown };
`
	syms := analyze(t, "src/model/c.ts", src)

	i := find(t, syms, "src/model/c.I")
	assert.Equal(t, parser.KindInterface, i.Kind)
	assert.True(t, i.Exported)
	assert.Equal(t, "An interface.", i.Doc)
	assert.Equal(t, 5, i.StartLine)
	assert.Equal(t, 7, i.EndLine)
	assert.Equal(t, "src/model/base.Base", rel(t, i, parser.RelExtends, "Base").TargetQualifiedName)
	assert.Equal(t, "src/geo/shapes.Shape", rel(t, i, parser.RelExtends, "Shape").TargetQualifiedName)

	assert.Equal(t, parser.KindType, find(t, syms, "src/model/c.Pair").Kind)
	assert.Equal(t, "export type Pair<T> = [T, T]", find(t, syms, "src/model/c.Pair").Signature)
	assert.Equal(t, parser.KindClass, find(t, syms, "src/model/c.Color").Kind)

	c := find(t, syms, "src/model/c.C")
	assert.Equal(t, parser.KindClass, c.Kind)
	assert.Equal(t, "src/model/base.Base", rel(t, c, parser.RelExtends, "Base").TargetQualifiedName)
	assert.Equal(t, "src/model/c.I", rel(t, c, parser.RelImplements, "I").TargetQualifiedName)

	secret := find(t, syms, "src/model/c.C.secret")
	assert.Equal(t, parser.KindMethod, secret.Kind)
	assert.False(t, secret.Exported)
	assert.Equal(t, "src/model/c.C.run", rel(t, secret, parser.RelCall, "run").TargetQualifiedName)

	run := find(t, syms, "src/model/c.C.run")
	assert.Equal(t, parser.KindMethod, run.Kind)
	assert.Equal(t, "C", run.Owner)
	assert.True(t, run.Exported)
	assert.Equal(t, "src/geo/shapes.Point", rel(t, run, parser.RelUsesType, "Point").TargetQualifiedName)

	assert.Equal(t, parser.KindMethod, find(t, syms, "src/model/c.C.build").Kind)
	assert.Equal(t, parser.KindFunction, find(t, syms, "src/model/c.dec").Kind)

	hidden := find(t, syms, "src/model/c.hidden")
	assert.Equal(t, parser.KindConstant, hidden.Kind)
	assert.True(t, hidden.Exported, "export { hidden as shown }")
}

func TestTSX(t *testing.T) {
	src := `import Button from './Button';
import * as UI from '../ui';
import { useState } from 'react';

export const App = () => {
  const [n, setN] = useState(0);
  return <div onClick={() => setN(n + 1)}><Button /><UI.Card /><Unknown /></div>;
};
`
	syms := analyze(t, "src/pages/App.tsx", src)
	app := find(t, syms, "src/pages/App.App")
	assert.Equal(t, parser.KindFunction, app.Kind)
	assert.True(t, app.Exported)
	assert.Equal(t, "react.useState", rel(t, app, parser.RelCall, "useState").TargetQualifiedName)
	assert.Equal(t, "src/pages/Button.default", rel(t, app, parser.RelUsesType, "Button").TargetQualifiedName)
	assert.Equal(t, "src/ui.Card", rel(t, app, parser.RelUsesType, "Card").TargetQualifiedName)
	assert.Equal(t, "", rel(t, app, parser.RelUsesType, "Unknown").TargetQualifiedName)
	assert.Equal(t, 7, rel(t, app, parser.RelUsesType, "Button").Line)
	assert.Equal(t, "src/pages/App.setN", "src/pages/App."+rel(t, app, parser.RelCall, "setN").TargetName)
	assert.Equal(t, "", rel(t, app, parser.RelCall, "setN").TargetQualifiedName, "local const, not top-level")
}

func TestVueOptionsAPI(t *testing.T) {
	src := `<template>
  <div @click="go">{{ label }}</div>
</template>

<script>
import api from '../services/api';

export default {
  name: 'TodoItem',
  data() { return { label: '' }; },
  computed: {
    label() { return this.raw; },
  },
  methods: {
    /** Go somewhere. */
    go() { this.stop(); api.fetch(); },
    stop: function () {},
  },
};
</script>
`
	syms := analyze(t, "src/components/todo-item.vue", src)

	tpl := find(t, syms, "src/components/todo-item.TodoItem")
	assert.Equal(t, parser.KindTemplate, tpl.Kind)
	assert.Equal(t, 1, tpl.StartLine)
	assert.Equal(t, 3, tpl.EndLine)
	assert.Contains(t, tpl.Content, "@click")

	for _, s := range syms {
		assert.NotEqual(t, parser.KindClass, s.Kind, "the options object itself is not a symbol")
	}
	go_ := find(t, syms, "src/components/todo-item.TodoItem.go")
	assert.Equal(t, parser.KindMethod, go_.Kind)
	assert.Equal(t, "TodoItem", go_.Owner)
	assert.Equal(t, "Go somewhere.", go_.Doc)
	assert.Equal(t, 16, go_.StartLine)
	assert.Equal(t, "src/components/todo-item.TodoItem.stop", rel(t, go_, parser.RelCall, "stop").TargetQualifiedName)
	assert.Equal(t, "src/services/api.default.fetch", rel(t, go_, parser.RelCall, "fetch").TargetQualifiedName)
	assert.Equal(t, 16, rel(t, go_, parser.RelCall, "stop").Line)

	assert.Equal(t, parser.KindMethod, find(t, syms, "src/components/todo-item.TodoItem.stop").Kind)
	assert.Equal(t, parser.KindMethod, find(t, syms, "src/components/todo-item.TodoItem.label").Kind)
	assert.Equal(t, parser.KindMethod, find(t, syms, "src/components/todo-item.TodoItem.data").Kind)
}

func TestVueScriptSetupTS(t *testing.T) {
	src := `<script setup lang="ts">
import { ref } from 'vue';
import { load } from './store';

interface Props { id: number }
const count = ref(0);
function reload() { load(count.value); }
</script>

<template><button @click="reload">{{ count }}</button></template>
`
	syms := analyze(t, "src/Counter.vue", src)
	props := find(t, syms, "src/Counter.Props")
	assert.Equal(t, parser.KindInterface, props.Kind)
	assert.Equal(t, 5, props.StartLine)

	count := find(t, syms, "src/Counter.count")
	assert.Equal(t, parser.KindConstant, count.Kind)
	assert.Equal(t, 6, count.StartLine)

	reload := find(t, syms, "src/Counter.reload")
	assert.Equal(t, parser.KindFunction, reload.Kind)
	assert.Equal(t, 7, reload.StartLine)
	assert.Equal(t, "src/store.load", rel(t, reload, parser.RelCall, "load").TargetQualifiedName)
	assert.Equal(t, 7, rel(t, reload, parser.RelCall, "load").Line)

	tpl := find(t, syms, "src/Counter.Counter")
	assert.Equal(t, parser.KindTemplate, tpl.Kind)
	assert.Equal(t, 10, tpl.StartLine)
}

func TestMalformed(t *testing.T) {
	cases := map[string]string{
		"broken.js":    "export function ok() {}\nfunction broken( {\nclass {{{\n",
		"empty.ts":     "",
		"garbage.js":   "}}}}}} ((( import from",
		"noscript.vue": "<template><p/></template>",
		"junk.vue":     "<script lang=\"ts\">export const x: = ;</script>",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(root, name), Root: root, Source: []byte(src)})
			require.NoError(t, err)
			for _, s := range syms {
				assert.NotEmpty(t, s.QualifiedName)
				assert.GreaterOrEqual(t, s.EndLine, s.StartLine)
			}
			if name == "broken.js" {
				assert.Equal(t, parser.KindFunction, find(t, syms, "broken.ok").Kind)
			}
		})
	}
}

func TestUnbalanced(t *testing.T) {
	// Brackets inside strings and comments do not count; unclosed ones do.
	strs := "export function f() { return '" + strings.Repeat("(", 300) + "'; } // " + strings.Repeat("[", 300) + "\n/* " + strings.Repeat("{", 300) + " */\n"
	assert.False(t, unbalanced([]byte(strs)))
	assert.Len(t, analyze(t, "strs.js", strs), 1)
	deep := strings.Repeat("export function f"+strings.Repeat("(", 2000)+"\n", 50)
	assert.True(t, unbalanced([]byte(deep)))
	assert.Empty(t, analyze(t, "deep.js", deep))
}

func TestOutsideRoot(t *testing.T) {
	syms, err := analyzer{}.Analyze(parser.File{Path: "/elsewhere/x.js", Root: root, Source: []byte("import { y } from './y';\nexport function f() { y(); }")})
	require.NoError(t, err)
	f := find(t, syms, "x.f")
	assert.Equal(t, "y.y", rel(t, f, parser.RelCall, "y").TargetQualifiedName)
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				syms := analyze(t, "src/app/main.js", esm)
				find(t, syms, "src/app/main.default.m")
			}
		}()
	}
	wg.Wait()
}

func TestRegistered(t *testing.T) {
	for _, p := range []string{"a.js", "b.mjs", "c.cjs", "d.jsx", "e.ts", "f.tsx", "g.vue", "H.VUE"} {
		a := parser.ForFile(p)
		require.NotNil(t, a, p)
		assert.Equal(t, "javascript", a.Language())
	}
}

// A default export is qualified as "default", the name importers bind it to,
// and a directory import resolves to its index module.
func TestDefaultExportAndIndexImport(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "src", "lib"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "src", "both"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "src", "both.ts"), nil, 0o644))
	src := `import { util } from './lib';
import { x } from './both';
export default function Button() { util(); x(); Button(); }
`
	syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "src", "Button.jsx"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	b := find(t, syms, "src/Button.default")
	assert.Equal(t, "Button", b.Name)
	assert.Equal(t, "src/lib/index.util", rel(t, b, parser.RelCall, "util").TargetQualifiedName)
	assert.Equal(t, "src/both.x", rel(t, b, parser.RelCall, "x").TargetQualifiedName, "file wins over directory")
	assert.Equal(t, "src/Button.default", rel(t, b, parser.RelCall, "Button").TargetQualifiedName)

	syms = analyze(t, "src/c.js", "function C() {}\nexport { C as default };\n")
	assert.Equal(t, "C", find(t, syms, "src/c.default").Name)
}

func TestLocalsShadowImports(t *testing.T) {
	syms := analyze(t, "src/a.js", `import {remove} from "./disk";
import fs from "./fs";
class Store { run() {} }
function invoke(remove) { remove(); }
function viaDefault(fs = 1) { fs.read(); }
function viaLocal() { const Store = make(); Store.run(); }
const arrow = ({remove: rm, fs}) => { rm(); fs.write(); };
function nested() { return [1].map((remove) => remove()); }
function real() { remove(); fs.stat(); Store.run(); }
class K { m(fs) { fs.x(); this.n(); } n() {} }
`)
	qn := func(fn, name string) string {
		return rel(t, find(t, syms, "src/a."+fn), parser.RelCall, name).TargetQualifiedName
	}
	assert.Equal(t, "", qn("invoke", "remove"))
	assert.Equal(t, "", qn("viaDefault", "read"))
	assert.Equal(t, "", qn("viaLocal", "run"))
	assert.Equal(t, "", qn("arrow", "rm"))
	assert.Equal(t, "", qn("arrow", "write"))
	assert.Equal(t, "", qn("nested", "remove"))
	assert.NotEmpty(t, qn("real", "remove"))
	assert.NotEmpty(t, qn("real", "stat"))
	assert.NotEmpty(t, qn("real", "run"))
	assert.Equal(t, "", qn("K.m", "x"))
	assert.Equal(t, "src/a.K.n", qn("K.m", "n"))
}

// A root-absolute specifier ("/js/api.js") names a file under the web root the
// page is served from: the nearest ancestor of the importing file that has it.
func TestRootAbsoluteImport(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "webui", "js"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "webui", "components", "chat"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "webui", "js", "api.js"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "webui", "js", "sleep.js"), nil, 0o644))
	src := `import { callJsonApi } from "/js/api.js";
import * as api from "/js/api.js";
import sleep from "/js/sleep";
import { y } from "/nowhere/y.js";
export function load() { callJsonApi(); api.fetchApi(); sleep(); y(); }
`
	syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "webui", "components", "chat", "store.js"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	l := find(t, syms, "webui/components/chat/store.load")
	assert.Equal(t, "webui/js/api.callJsonApi", rel(t, l, parser.RelCall, "callJsonApi").TargetQualifiedName)
	assert.Equal(t, "webui/js/api.fetchApi", rel(t, l, parser.RelCall, "fetchApi").TargetQualifiedName)
	assert.Equal(t, "webui/js/sleep.default", rel(t, l, parser.RelCall, "sleep").TargetQualifiedName)
	assert.Equal(t, "/nowhere/y.js.y", rel(t, l, parser.RelCall, "y").TargetQualifiedName, "not found: left as written")
}

// TypeScript type annotations, generic arguments and interface / alias
// bodies use the types they name, when an import or this file defines them.
func TestTypeScriptTypeUses(t *testing.T) {
	src := `import { ApiResponse, Plant } from '../types';
import * as m from '../model';

interface AuthCtx {
  user: AuthUser | null;
  plants: Plant[];
}

type Res<T> = ApiResponse<T> | m.Err;

export interface AuthUser { id: string }

export function load<T>(p: Plant, x: string): Promise<ApiResponse<T>> {
  const list = useState<Plant[]>([]);
  return fetchIt<m.Item>(p);
}
`
	syms := analyze(t, "src/hooks/use.ts", src)
	uses := func(qn string) []string {
		var out []string
		for _, r := range find(t, syms, qn).Relations {
			if r.Kind == parser.RelUsesType {
				out = append(out, fmt.Sprintf("%s@%d", r.TargetQualifiedName, r.Line))
			}
		}
		return out
	}
	assert.Equal(t, []string{"src/hooks/use.AuthUser@5", "src/types.Plant@6"}, uses("src/hooks/use.AuthCtx"))
	assert.Equal(t, []string{"src/types.ApiResponse@9", "src/model.Err@9"}, uses("src/hooks/use.Res"), "T is a type parameter")
	assert.ElementsMatch(t, []string{"src/types.Plant@14", "src/model.Item@15", "src/types.Plant@13", "src/types.ApiResponse@13"}, uses("src/hooks/use.load"))
}

// The methods of an object literal bound to a top-level const (an Alpine or
// Pinia store model) are methods of that const, with their calls.
func TestObjectLiteralMethods(t *testing.T) {
	src := `import * as api from './api';
const model = {
  items: [],
  async load() { await api.callJsonApi('/x'); this.render(); },
  render: () => draw(),
};
`
	syms := analyze(t, "src/store.js", src)
	load := find(t, syms, "src/store.model.load")
	assert.Equal(t, parser.KindMethod, load.Kind)
	assert.Equal(t, "model", load.Owner)
	assert.Equal(t, "src/api.callJsonApi", rel(t, load, parser.RelCall, "callJsonApi").TargetQualifiedName)
	assert.Equal(t, "src/store.model.render", rel(t, load, parser.RelCall, "render").TargetQualifiedName)
	find(t, syms, "src/store.model.render")
	find(t, syms, "src/store.model")
}

// Aliased imports resolve through the nearest tsconfig/jsconfig "paths"
// (comments and trailing commas allowed, relative "extends" followed) and,
// without a mapping, through the Nuxt/Vite conventions. Only an existing
// file counts; anything else stays as written.
func TestAliasImports(t *testing.T) {
	write := func(ws, rel, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(ws, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, rel), []byte(body), 0o644))
	}
	callee := func(t *testing.T, ws, file, src, fn string) map[string]string {
		syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, file), Root: ws, Source: []byte(src)})
		require.NoError(t, err)
		out := map[string]string{}
		for _, r := range find(t, syms, fn).Relations {
			if r.Kind == parser.RelCall {
				out[r.TargetName] = r.TargetQualifiedName
			}
		}
		return out
	}

	t.Run("tsconfig paths", func(t *testing.T) {
		ws := t.TempDir()
		write(ws, "tsconfig.base.json", `{
  // shared options
  "compilerOptions": { "paths": { "@/*": ["./*"], "#ui": ["./components/ui/index.ts"], }, },
}`)
		write(ws, "tsconfig.json", `{ "extends": "./tsconfig.base.json" }`)
		write(ws, "lib/utils.ts", "")
		write(ws, "components/ui/index.ts", "")
		write(ws, "pkg/web/tsconfig.json", `{ "compilerOptions": { "baseUrl": "src", "paths": { "lib/*": ["shared/*"] } } }`)
		write(ws, "pkg/web/src/shared/fmt.ts", "")
		got := callee(t, ws, "components/form.tsx", `import { cleanText } from "@/lib/utils";
import { Button } from "#ui";
import { gone } from "@/lib/missing";
import { Slot } from "@radix-ui/react-slot";
export function Form() { cleanText(); Button(); gone(); Slot(); }
`, "components/form.Form")
		assert.Equal(t, "lib/utils.cleanText", got["cleanText"])
		assert.Equal(t, "components/ui/index.Button", got["Button"])
		assert.Equal(t, "@/lib/missing.gone", got["gone"], "missing file: left as written")
		assert.Equal(t, "@radix-ui/react-slot.Slot", got["Slot"])
		got = callee(t, ws, "pkg/web/src/app.ts", `import { fmt } from "lib/fmt";
export function run() { fmt(); }
`, "pkg/web/src/app.run")
		assert.Equal(t, "pkg/web/src/shared/fmt.fmt", got["fmt"], "nearest tsconfig, paths under baseUrl")
	})

	t.Run("nuxt conventions", func(t *testing.T) {
		ws := t.TempDir()
		write(ws, "package.json", "{}")
		write(ws, "tsconfig.json", `{ "references": [{ "path": "./.nuxt/tsconfig.app.json" }], "files": [] }`)
		write(ws, "app/composables/users.ts", "")
		write(ws, "server/utils/db.ts", "")
		write(ws, "shared/types.ts", "")
		got := callee(t, ws, "app/pages/home.ts", `import { useUsers } from "~/composables/users";
import { db } from "~~/server/utils/db";
import { parse } from "#shared/types";
import { nope } from "~/composables/nope";
export function home() { useUsers(); db(); parse(); nope(); }
`, "app/pages/home.home")
		assert.Equal(t, "app/composables/users.useUsers", got["useUsers"])
		assert.Equal(t, "server/utils/db.db", got["db"])
		assert.Equal(t, "shared/types.parse", got["parse"])
		assert.Equal(t, "~/composables/nope.nope", got["nope"])
	})
}

// Calls in a top-level const initializer belong to the const, and
// `export default defineX(() => ...)` (Nuxt middleware, plugins, server
// routes) is a default symbol owning the calls of its callback. An object
// literal's method calls stay with the methods.
func TestTopLevelInitializerCalls(t *testing.T) {
	syms := analyze(t, "app/m.ts", `import { useFormattedDateTime } from './time';
function filterClasses(x) { return x }
const sanitizer = sanitize({ class: filterClasses('a') });
export const createdAt = useFormattedDateTime(1);
const model = { load() { fetchIt() } };
export default defineNuxtRouteMiddleware((to) => { handleAuth(to) });
function handleAuth(t) {}
`)
	assert.Equal(t, "app/m.filterClasses", rel(t, find(t, syms, "app/m.sanitizer"), parser.RelCall, "filterClasses").TargetQualifiedName)
	assert.Equal(t, "app/time.useFormattedDateTime", rel(t, find(t, syms, "app/m.createdAt"), parser.RelCall, "useFormattedDateTime").TargetQualifiedName)
	assert.Empty(t, find(t, syms, "app/m.model").Relations, "methods own their calls")
	rel(t, find(t, syms, "app/m.model.load"), parser.RelCall, "fetchIt")
	d := find(t, syms, "app/m.default")
	assert.Equal(t, parser.KindFunction, d.Kind)
	assert.Equal(t, "app/m.handleAuth", rel(t, d, parser.RelCall, "handleAuth").TargetQualifiedName)
}

// Methods of a top-level object literal are exported only with their const,
// and `const C = defineComponent({...})` yields C's component methods.
func TestObjectMethodsExportAndDefineComponentConst(t *testing.T) {
	syms := analyze(t, "m.ts", `const model = { load() {} };
export const store = { save() {} };
const C = defineComponent({ methods: { go() { this.stop() } }, stop() {} });
`)
	assert.False(t, find(t, syms, "m.model.load").Exported)
	assert.True(t, find(t, syms, "m.store.save").Exported)
	assert.Equal(t, parser.KindMethod, find(t, syms, "m.C.go").Kind)
	find(t, syms, "m.C.stop")
	assert.Empty(t, find(t, syms, "m.C").Relations, "the component's methods own their calls")
}

// An imported name resolves through the target module's re-exports to the
// module that declares it: export * from, export { a as b } from, and local
// alias exports (hono's `export { Hono as HonoBase }`).
func TestBarrelReexports(t *testing.T) {
	ws := t.TempDir()
	write := func(rel, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(ws, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, rel), []byte(body), 0o644))
	}
	write("src/helper/ssg/index.ts", "export * from './utils'\nexport * from './ssg'\nexport { defaultPlugin as dp, redirectPlugin } from './plugins'\nexport { default as Page } from './page'\nexport * as ns from './ns'\nexport const own = 1\n")
	write("src/helper/ssg/utils.ts", "export const other = 1\n")
	write("src/helper/ssg/ssg.ts", "export const toSSG = () => {}\nexport * from './ssg'\n") // self-cycle
	write("src/helper/ssg/plugins.ts", "export const defaultPlugin = {}\nexport const redirectPlugin = {}\n")
	write("src/helper/ssg/page.tsx", "export default function Page() {}\n")
	write("src/hono-base.ts", "import { Router } from './router'\nclass Hono {}\nexport { Hono as HonoBase, Router as R }\n")
	write("src/router.ts", "export class Router {}\n")
	write("src/def.ts", "import { Router } from './router'\nexport default Router\n")
	src := `import { toSSG as baseToSSG, dp, redirectPlugin, Page, own, missing } from './helper/ssg';
import { HonoBase, R } from './hono-base';
import fallback from './helper/ssg';
import def from './def';
export class Hono extends HonoBase {}
export function run() { baseToSSG(); dp(); redirectPlugin(); Page(); own(); missing(); R(); fallback(); def(); }
`
	syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "src", "app.ts"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	run := find(t, syms, "src/app.run")
	for name, want := range map[string]string{
		"baseToSSG":      "src/helper/ssg/ssg.toSSG",
		"dp":             "src/helper/ssg/plugins.defaultPlugin",
		"redirectPlugin": "src/helper/ssg/plugins.redirectPlugin",
		"Page":           "src/helper/ssg/page.default",
		"own":            "src/helper/ssg/index.own",
		"missing":        "src/helper/ssg/index.missing", // nothing declares it: unchanged
		"R":              "src/router.Router",
		"fallback":       "src/helper/ssg/index.default",
		"def":            "src/router.Router",
	} {
		assert.Equal(t, want, rel(t, run, parser.RelCall, name).TargetQualifiedName, name)
	}
	assert.Equal(t, "src/hono-base.Hono", rel(t, find(t, syms, "src/app.Hono"), parser.RelExtends, "HonoBase").TargetQualifiedName)

	// An edited barrel is re-read (mtime + size).
	write("src/helper/ssg/index.ts", "export { toSSG as missing } from './ssg'\n")
	syms, err = analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "src", "app.ts"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	assert.Equal(t, "src/helper/ssg/ssg.toSSG", rel(t, find(t, syms, "src/app.run"), parser.RelCall, "missing").TargetQualifiedName)
}

// Top-level expression statements of a Vue <script setup> belong to the
// component, the file's default export, named "default".
func TestVueScriptSetupTopLevelCalls(t *testing.T) {
	src := `<template><div/></template>

<script setup lang="ts">
import { watch } from 'vue';
import { fetchAccount } from './api';
import type { SearchResult } from './types';
defineProps<{ result: SearchResult }>()
watch(() => 1, () => fetchAccount())
</script>
`
	syms := analyze(t, "src/Account.vue", src)
	d := find(t, syms, "src/Account.default")
	assert.Equal(t, parser.KindFunction, d.Kind)
	assert.True(t, d.Exported)
	assert.Equal(t, "", d.Owner)
	assert.Equal(t, 3, d.StartLine)
	assert.Equal(t, 9, d.EndLine)
	assert.Equal(t, "src/api.fetchAccount", rel(t, d, parser.RelCall, "fetchAccount").TargetQualifiedName)
	assert.Equal(t, "vue.watch", rel(t, d, parser.RelCall, "watch").TargetQualifiedName)
	assert.Equal(t, "src/types.SearchResult", rel(t, d, parser.RelUsesType, "SearchResult").TargetQualifiedName)

	// An existing default symbol gets the calls instead of a second one.
	src = `<script>
export default defineNuxtComponent(() => {})
</script>
<script setup>
import { go } from './go';
go()
</script>
`
	syms = analyze(t, "src/Two.vue", src)
	n := 0
	for _, s := range syms {
		if s.Name == "default" {
			n++
			assert.Equal(t, "src/go.go", rel(t, s, parser.RelCall, "go").TargetQualifiedName)
		}
	}
	assert.Equal(t, 1, n)

	// Plain scripts keep top-level expression statements unattributed.
	syms = analyze(t, "src/a.test.js", "import { it } from 'vitest';\nit('x', () => {});\n")
	assert.Empty(t, syms)
}

// A package specifier is never read as a workspace file, even when the root
// happens to hold a file of that name, directly or through a re-export.
func TestBarrelSkipsPackageSpecifiers(t *testing.T) {
	ws := t.TempDir()
	for rel, body := range map[string]string{
		"react.ts":  "export * from './local'\n",
		"local.ts":  "export const useState = 1\n",
		"lib/re.ts": "export * from 'react'\nexport { useMemo } from 'react'\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(ws, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, rel), []byte(body), 0o644))
	}
	src := "import { useState } from 'react'\nimport { useMemo, useState as s2 } from './lib/re'\nexport function run() { useState(); useMemo(); s2() }\n"
	syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "app.ts"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	run := find(t, syms, "app.run")
	// Its module is not the root file's either, so the index cannot bind it exactly there.
	assert.Equal(t, "node_modules/react.useState", rel(t, run, parser.RelCall, "useState").TargetQualifiedName)
	assert.Equal(t, "node_modules/react.useMemo", rel(t, run, parser.RelCall, "useMemo").TargetQualifiedName)
	assert.Equal(t, "lib/re.useState", rel(t, run, parser.RelCall, "s2").TargetQualifiedName) // export * from a package: not followed
	// Without a root file of that name the package keeps its plain name.
	syms, err = analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "app.ts"), Root: ws, Source: []byte("import { ref } from 'vue'\nexport function v() { ref() }\n")})
	require.NoError(t, err)
	assert.Equal(t, "vue.ref", rel(t, find(t, syms, "app.v"), parser.RelCall, "ref").TargetQualifiedName)
}

// '.' and '..' are the current and parent directory, not package names: they
// resolve to that directory's index module and through its re-exports.
func TestDotSpecifiers(t *testing.T) {
	ws := t.TempDir()
	for rel, body := range map[string]string{
		"pkg/index.ts": "export { Solo as SoloT } from './t1'\nexport const delta = 1\n",
		"pkg/t1.ts":    "export type Solo = {}\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(ws, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, rel), []byte(body), 0o644))
	}
	src := "import type { SoloT } from '.'\nimport { delta as d2 } from '.'\nexport function u1(a: SoloT) { d2() }\n"
	syms, err := analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "pkg", "u.ts"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	u1 := find(t, syms, "pkg/u.u1")
	assert.Equal(t, "pkg/index.delta", rel(t, u1, parser.RelCall, "d2").TargetQualifiedName)
	assert.Equal(t, "pkg/t1.Solo", rel(t, u1, parser.RelUsesType, "SoloT").TargetQualifiedName)

	src = "import { delta as d5 } from '..'\nexport function w() { d5() }\n"
	syms, err = analyzer{}.Analyze(parser.File{Path: filepath.Join(ws, "pkg", "sub", "w.ts"), Root: ws, Source: []byte(src)})
	require.NoError(t, err)
	assert.Equal(t, "pkg/index.delta", rel(t, find(t, syms, "pkg/sub/w.w"), parser.RelCall, "d5").TargetQualifiedName)
}

// `await f<T>(...)` parses as a call whose callee is the await expression:
// f is still the call target and T a type use.
func TestAwaitGenericCall(t *testing.T) {
	syms := analyze(t, "app/p.ts", `import { useAsyncIDBKeyval } from './idb';
import type { UserLogin } from './types';
export async function setup() {
  const { readIDB } = await useAsyncIDBKeyval<UserLogin[]>(key(1));
  await useAsyncData<UserLogin>(load());
}
function key(n) {}
`)
	s := find(t, syms, "app/p.setup")
	assert.Equal(t, "app/idb.useAsyncIDBKeyval", rel(t, s, parser.RelCall, "useAsyncIDBKeyval").TargetQualifiedName)
	assert.Equal(t, "app/p.key", rel(t, s, parser.RelCall, "key").TargetQualifiedName)
	rel(t, s, parser.RelCall, "useAsyncData")
	rel(t, s, parser.RelCall, "load")
	assert.Equal(t, "app/types.UserLogin", rel(t, s, parser.RelUsesType, "UserLogin").TargetQualifiedName)
}

// The type annotation of a top-level const / let is a type use of it.
func TestTopLevelVariableTypeAnnotation(t *testing.T) {
	syms := analyze(t, "app/v.ts", `import type { Foo, Bar } from './types';
const c1: Foo[] = [];
export const c2: Foo = makeIt();
let c3: Bar = { load() {} };
`)
	for _, qn := range []string{"app/v.c1", "app/v.c2"} {
		assert.Equal(t, "app/types.Foo", rel(t, find(t, syms, qn), parser.RelUsesType, "Foo").TargetQualifiedName, qn)
	}
	rel(t, find(t, syms, "app/v.c2"), parser.RelCall, "makeIt")
	assert.Equal(t, "app/types.Bar", rel(t, find(t, syms, "app/v.c3"), parser.RelUsesType, "Bar").TargetQualifiedName)
}

// The <script> and <script setup> blocks of one SFC are one module: each
// sees the other's imports and top-level names.
func TestVueTwoScriptBlocksShareNames(t *testing.T) {
	syms := analyze(t, "src/Two.vue", `<script lang="ts">
import { api } from './api';
export function helper() { fromSetup() }
</script>
<script setup lang="ts">
function fromSetup() {}
helper()
const x = api()
</script>
`)
	d := find(t, syms, "src/Two.default")
	assert.Equal(t, "src/Two.helper", rel(t, d, parser.RelCall, "helper").TargetQualifiedName)
	assert.Equal(t, "src/api.api", rel(t, find(t, syms, "src/Two.x"), parser.RelCall, "api").TargetQualifiedName)
	assert.Equal(t, "src/Two.fromSetup", rel(t, find(t, syms, "src/Two.helper"), parser.RelCall, "fromSetup").TargetQualifiedName)
}

// A chained call split over lines is on the line of its method name, not on
// the line where the chain starts.
func TestChainedCallLine(t *testing.T) {
	syms := analyze(t, "src/chain.ts", "import { lib } from './lib'\nexport function chain() {\n  return lib\n    .useThing('x')\n}\n")
	assert.Equal(t, 4, rel(t, find(t, syms, "src/chain.chain"), parser.RelCall, "useThing").Line)
}
