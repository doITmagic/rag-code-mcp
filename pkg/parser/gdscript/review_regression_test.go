package gdscript

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func TestConnectHandlerKeepsTargetReceiver(t *testing.T) {
	for _, tc := range []struct {
		name, call, receiver, qualified string
	}{
		{"bare callable", "pressed.connect(on_pressed)", "", "Listener.on_pressed"},
		{"self callable", "pressed.connect(self.on_pressed)", "self", "Listener.on_pressed"},
		{"other callable", "pressed.connect(other.on_pressed)", "other", ""},
		{"node callable", "pressed.connect($Other.on_pressed)", "$Other", ""},
		{"class callable", "pressed.connect(Other.on_pressed)", "Other", "Other.on_pressed"},
		{"bare connect", "connect(\"pressed\", other.on_pressed)", "other", ""},
		{"Godot 3 self", "connect(\"pressed\", self, \"on_pressed\")", "self", "Listener.on_pressed"},
		{"Godot 3 other", "$Button.connect(\"pressed\", other, \"on_pressed\")", "other", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "class_name Listener\nextends Node\nsignal pressed\nfunc wire():\n\t" + tc.call + "\nfunc on_pressed():\n\tpass\n"
			wire := byQualified(t, analyze(t, "listener.gd", src), "Listener.wire")
			var handlers []pkgParser.Relation
			for _, rel := range wire.Relations {
				if rel.Type == pkgParser.RelCalls && rel.TargetName == "on_pressed" {
					handlers = append(handlers, rel)
				}
			}
			want := pkgParser.Relation{TargetName: "on_pressed", Type: pkgParser.RelCalls, Receiver: tc.receiver, TargetQualifiedName: tc.qualified}
			if len(handlers) != 1 || handlers[0] != want {
				t.Fatalf("handlers = %+v, want %+v", handlers, want)
			}
		})
	}
}

func TestPathBaseUsesDeclaredClassIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, baseSource, wantBase string
	}{
		{"class_name", "class_name BaseActor\nextends Node\n", "BaseActor"},
		{"filename fallback", "extends Node\n", "base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"project.godot": "config_version=5\n",
				"base.gd":       tc.baseSource + "func work():\n\tpass\n",
				"child.gd":      "class_name Child\nextends \"res://base.gd\"\nfunc run():\n\twork()\n\tself.work()\n\tsuper.work()\nclass Inner extends \"res://base.gd\":\n\tfunc run():\n\t\twork()\n",
			}
			for name, source := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			res, err := NewAnalyzer().Analyze(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			byQualified(t, res.Symbols, tc.wantBase+".work")
			for _, class := range []string{"Child", "Child.Inner"} {
				cls := byQualified(t, res.Symbols, class)
				if !hasRelation(cls, pkgParser.Relation{TargetName: tc.wantBase, Type: pkgParser.RelInheritance}) {
					t.Errorf("%s inheritance = %+v", class, cls.Relations)
				}
				run := byQualified(t, res.Symbols, class+".run")
				receivers := []string{""}
				if class == "Child" {
					receivers = append(receivers, "self", "super")
				}
				for _, receiver := range receivers {
					want := pkgParser.Relation{TargetName: "work", Type: pkgParser.RelCalls, Receiver: receiver, TargetQualifiedName: tc.wantBase + ".work"}
					if !hasRelation(run, want) {
						t.Errorf("%s missing %+v; got %+v", class, want, run.Relations)
					}
				}
			}
		})
	}
}
