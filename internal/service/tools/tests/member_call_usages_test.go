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
