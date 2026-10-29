package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/gdscript"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/javascript"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// A member call on a value of unknown type may reach any method of that
// name; a receiver naming another class does not reach the target.
func TestMemberCallsReachResolvedMethod(t *testing.T) {
	for _, tc := range []struct {
		lang     string
		analyzer parser.Analyzer
		files    map[string]string
		file     string
		symbol   string
		want     string
	}{
		{"php", php.NewAnalyzer(), map[string]string{
			"Dotenv.php": "<?php\nnamespace Dotenv;\nclass Dotenv {\n  public function load() { return 1; }\n}\n",
			"Other.php":  "<?php\nnamespace App;\nclass Other {\n  public static function load() { return 2; }\n}\n",
			"Boot.php":   "<?php\nnamespace App;\nclass Boot {\n  public function run($dotenv) { $dotenv->load(); }\n  public function other() { Other::load(); }\n}\n",
		}, "Dotenv.php", "Dotenv\\Dotenv::load", "run"},
		{"ts", javascript.NewCodeAnalyzer(), map[string]string{
			"ky.ts":    "export class Ky {\n  static create(opts: any) { return new Ky(); }\n}\n",
			"other.ts": "export class OtherClass {\n  static create() { return 1; }\n}\n",
			"main.ts":  "import { Ky } from './ky';\nimport { OtherClass } from './other';\nexport function makeClient() { return Ky.create({}); }\nexport function makeOther() { return OtherClass.create(); }\n",
		}, "ky.ts", "Ky.create", "makeClient"},
		{"gd", gdscript.NewAnalyzer(), map[string]string{
			"project.godot": "config_version=5\n",
			"blend.gd":      "class_name Blend\nextends Node\n\nfunc calculate_steering(x):\n\treturn x\n",
			"agent.gd":      "extends Node\n\nvar blend: Blend\n\nfunc _process(delta):\n\tblend.calculate_steering(delta)\n",
		}, "blend.gd", "Blend.calculate_steering", "_process"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			dir, store := indexFixture(t, tc.lang, tc.analyzer, tc.files)
			eng := setupTestEngine(store)
			t.Cleanup(eng.Close)
			path := filepath.Join(dir, tc.file)
			if got := pythonCallers(t, tools.NewCallHierarchyTool(eng), path, tc.symbol); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("callers of %s = %v, want [%s]", tc.symbol, got, tc.want)
			}
			text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": tc.symbol, "file_path": path})
			if err != nil {
				t.Fatal(err)
			}
			var response struct{ Data []tools.UsageResult }
			if err := json.Unmarshal([]byte(text), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Data) != 1 || response.Data[0].Name != tc.want {
				t.Errorf("usages of %s want [%s]: %s", tc.symbol, tc.want, text)
			}
		})
	}
}

// Object.create() and console.log() call a global, not an instance of a
// project class, so they are no callers of Ky.create or Ky.log.
func TestJSGlobalReceiverCallsNoProjectMethod(t *testing.T) {
	dir, store := indexFixture(t, "ts", javascript.NewCodeAnalyzer(), map[string]string{
		"ky.ts":   "export class Ky {\n  static create(opts: any) { return new Ky(); }\n  log(v: any) { return v; }\n}\n",
		"main.ts": "import { Ky } from './ky';\nexport function makeClient() { return Ky.create({}); }\nexport function emptyRecord() { return Object.create(null); }\nexport function report(v: any) { console.log(v); }\nexport function useInstance(client: Ky) { return client.log(1); }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	tool := tools.NewCallHierarchyTool(eng)
	path := filepath.Join(dir, "ky.ts")
	for symbol, want := range map[string]string{"Ky.create": "makeClient", "Ky.log": "useInstance"} {
		if got := pythonCallers(t, tool, path, symbol); !slices.Equal(got, []string{want}) {
			t.Errorf("callers of %s = %v, want [%s]", symbol, got, want)
		}
	}
}

// import * as money from './money' makes money.f() a call of money.ts's f.
func TestJSNamespaceImportCallReachesFunction(t *testing.T) {
	dir, store := indexFixture(t, "ts", javascript.NewCodeAnalyzer(), map[string]string{
		"money.ts": "export function centsToDisplay(c: number) { return c; }\n",
		"main.ts":  "import * as money from './money';\nexport function showTotal(c: number) { return money.centsToDisplay(c); }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	if got := pythonCallers(t, tools.NewCallHierarchyTool(eng), filepath.Join(dir, "money.ts"), "centsToDisplay"); !slices.Equal(got, []string{"showTotal"}) {
		t.Errorf("callers of centsToDisplay = %v, want [showTotal]", got)
	}
}

// $this->m() may call a method the class inherits from a parent or a trait.
func TestPHPThisCallReachesInheritedMethod(t *testing.T) {
	dir, store := indexFixture(t, "php", php.NewAnalyzer(), map[string]string{
		"AbstractStore.php": "<?php\nnamespace Shop;\nabstract class AbstractStore {\n  protected function canonicalOrderId($id) { return $id; }\n}\n",
		"Auditable.php":     "<?php\nnamespace Shop;\ntrait Auditable {\n  protected function recordAuditEntry($e) { return $e; }\n}\n",
		"OrderStore.php":    "<?php\nnamespace Shop;\nclass OrderStore extends AbstractStore {\n  use Auditable;\n  public function storeOrder($id) {\n    $this->recordAuditEntry($id);\n    return $this->canonicalOrderId($id);\n  }\n}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	tool := tools.NewCallHierarchyTool(eng)
	for file, symbol := range map[string]string{"AbstractStore.php": "canonicalOrderId", "Auditable.php": "recordAuditEntry"} {
		if got := pythonCallers(t, tool, filepath.Join(dir, file), symbol); !slices.Equal(got, []string{"storeOrder"}) {
			t.Errorf("callers of %s = %v, want [storeOrder]", symbol, got)
		}
	}
}

// A member call reaches no free function, and in PHP and JS/TS a bare call
// reaches no method: JSON.parse() is not a caller of a project's parse().
func TestMemberAndFreeCallsKeepTheirTargetKind(t *testing.T) {
	for _, tc := range []struct {
		lang     string
		analyzer parser.Analyzer
		files    map[string]string
		file     string
		symbol   string
		want     string
	}{
		{"php", php.NewAnalyzer(), map[string]string{
			"helpers.php": "<?php\nnamespace App;\nfunction format($x) { return $x; }\n",
			"Boot.php":    "<?php\nnamespace App;\nclass Boot {\n  public function run($date) { return $date->format('Y'); }\n  public function free() { return format(1); }\n}\n",
		}, "helpers.php", "format", "free"},
		{"ts-free", javascript.NewCodeAnalyzer(), map[string]string{
			"utils.ts": "export function parse(s: string) { return s; }\n",
			"main.ts":  "import { parse } from './utils';\nexport function a(s: string) { return JSON.parse(s); }\nexport function b(s: string) { return parse(s); }\n",
		}, "utils.ts", "parse", "b"},
		{"ts-method", javascript.NewCodeAnalyzer(), map[string]string{
			"ky.ts": "export class Ky {\n  static create() { return new Ky(); }\n}\n",
			"a.ts":  "export function create() { return 1; }\n",
			"b.ts":  "export function create() { return 2; }\n",
			"c.ts":  "import { create } from './a';\nexport function useFree() { return create(); }\nexport function useKy(k: any) { return k.create(); }\n",
		}, "ky.ts", "Ky.create", "useKy"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			dir, store := indexFixture(t, tc.lang, tc.analyzer, tc.files)
			eng := setupTestEngine(store)
			t.Cleanup(eng.Close)
			path := filepath.Join(dir, tc.file)
			if got := pythonCallers(t, tools.NewCallHierarchyTool(eng), path, tc.symbol); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("callers of %s = %v, want [%s]", tc.symbol, got, tc.want)
			}
		})
	}
}
