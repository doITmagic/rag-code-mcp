package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/gdscript"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

func TestFindUsagesOfInstancedScene(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"project.godot": "config_version=5\n",
		"enemy.tscn":    "[gd_scene format=3]\n[node name=\"Enemy\" type=\"Node\"]\n",
		"other.tscn":    "[gd_scene format=3]\n[node name=\"Other\" type=\"Node\"]\n",
		"level.tscn":    "[gd_scene load_steps=2 format=3]\n[ext_resource type=\"PackedScene\" path=\"res://enemy.tscn\" id=\"1\"]\n[node name=\"Level\" type=\"Node\"]\n[node name=\"Enemy\" parent=\".\" instance=ExtResource(\"1\")]\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := gdscript.NewAnalyzer().Analyze(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var points []storage.SearchResult
	for _, symbol := range res.Symbols {
		data, err := json.Marshal(symbol)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		points = append(points, storage.SearchResult{Point: storage.Point{ID: symbol.Name, Payload: payload}})
	}
	for _, indexedTarget := range []bool{true, false} {
		t.Run(map[bool]string{true: "indexed target", false: "unindexed target"}[indexedTarget], func(t *testing.T) {
			store := &mockVectorStore{ExactSearchFunc: func(_ context.Context, col string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
				if !strings.HasSuffix(col, "-gdscript") {
					return nil, nil
				}
				var matches []storage.SearchResult
				for _, point := range points {
					if !indexedTarget && point.Point.ID == "enemy.tscn" {
						continue
					}
					match := true
					for key, value := range filters {
						if key == "relations[].target_name" {
							match = false
							relations, _ := point.Point.Payload["relations"].([]interface{})
							for _, raw := range relations {
								if raw.(map[string]interface{})["target_name"] == value {
									match = true
								}
							}
						} else {
							match = point.Point.Payload[key] == value
						}
						if !match {
							break
						}
					}
					if match {
						matches = append(matches, point)
					}
				}
				return matches, nil
			}}
			tool := tools.NewFindUsagesTool(setupTestEngine(store))
			for _, tc := range []struct {
				name string
				want int
			}{{"enemy.tscn", 1}, {"other.tscn", 0}} {
				result, err := tool.Execute(context.Background(), map[string]interface{}{"symbol_name": tc.name, "file_path": filepath.Join(root, "level.tscn")})
				if err != nil {
					t.Fatal(err)
				}
				var response struct {
					Status string
					Data   []tools.UsageResult
				}
				if err := json.Unmarshal([]byte(result), &response); err != nil {
					t.Fatal(err)
				}
				if response.Status != "success" || len(response.Data) != tc.want {
					t.Fatalf("%s: %s", tc.name, result)
				}
				if tc.want == 1 && (response.Data[0].Name != "level.tscn" || strings.Join(response.Data[0].MatchReason, ",") != "dependency") {
					t.Fatalf("wrong scene usage: %+v", response.Data)
				}
			}
		})
	}
}
