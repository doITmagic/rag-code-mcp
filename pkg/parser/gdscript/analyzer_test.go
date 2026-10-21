package gdscript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

const playerScript = `@tool
class_name Player
extends CharacterBody2D
## Player controller.
## Handles input.

signal health_changed(old_value: int, new_value: int)
signal died

enum State { IDLE, RUN, JUMP = 5 }
enum { A, B }

const MAX_SPEED := 300.0
@export var speed: float = 200.0
@onready var weapon: Weapon = $Weapon
var _hp := 100:
	set(value):
		_hp = clampi(value, 0, 100)
	get:
		return _hp

static var count = 0

@export_group("Debug")
var debug = false
@export_range(0, 10) var jump: int = 3

## Moves the player.
func move(delta: float, dir := Vector2.ZERO) -> void:
	velocity = dir * speed
	move_and_slide()
	weapon.aim(dir)
	_take_damage(1)
	self.heal(1)
	super.move(delta)
	Utils.helper()

func heal(n: int) -> void:
	_hp += n

static func create() -> Player:
	return Player.new()

func _take_damage(n: int):
	health_changed.emit(_hp, _hp - n)
	emit_signal("died")
	$Button.pressed.connect(_on_button_pressed)

func _on_button_pressed():
	pass

class Inner extends Node:
	## An inner helper.
	var x = 1
	func foo():
		self.bar()
	func bar():
		pass
`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func analyze(t *testing.T, name, content string) []pkgParser.Symbol {
	t.Helper()
	path := writeFile(t, name, content)
	res, err := NewAnalyzer().Analyze(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "gdscript" {
		t.Fatalf("language = %q", res.Language)
	}
	for _, s := range res.Symbols {
		if s.FilePath != path {
			t.Fatalf("symbol %s has file %s, want %s", s.Name, s.FilePath, path)
		}
		if s.Language != "gdscript" {
			t.Fatalf("symbol %s has language %q", s.Name, s.Language)
		}
	}
	return res.Symbols
}

func byQualified(t *testing.T, symbols []pkgParser.Symbol, qn string) pkgParser.Symbol {
	t.Helper()
	for _, s := range symbols {
		if s.QualifiedName == qn {
			return s
		}
	}
	var names []string
	for _, s := range symbols {
		names = append(names, s.QualifiedName)
	}
	t.Fatalf("no symbol %s in %v", qn, names)
	return pkgParser.Symbol{}
}

func hasRelation(s pkgParser.Symbol, want pkgParser.Relation) bool {
	for _, r := range s.Relations {
		if r == want {
			return true
		}
	}
	return false
}

func TestScriptClass(t *testing.T) {
	symbols := analyze(t, "player.gd", playerScript)
	cls := symbols[0]
	if cls.Name != "Player" || cls.Type != pkgParser.Class || cls.QualifiedName != "Player" {
		t.Fatalf("script class = %+v", cls)
	}
	if cls.Signature != "class_name Player extends CharacterBody2D" {
		t.Errorf("signature = %q", cls.Signature)
	}
	if cls.Docstring != "Player controller.\nHandles input." {
		t.Errorf("docstring = %q", cls.Docstring)
	}
	if cls.Metadata["is_tool"] != true || cls.Metadata["extends"] != "CharacterBody2D" {
		t.Errorf("metadata = %v", cls.Metadata)
	}
	if !hasRelation(cls, pkgParser.Relation{TargetName: "CharacterBody2D", Type: pkgParser.RelInheritance}) {
		t.Errorf("missing inheritance: %v", cls.Relations)
	}
	for _, s := range symbols {
		if s.Package != "Player" {
			t.Errorf("%s package = %q, want Player", s.QualifiedName, s.Package)
		}
	}
}

func TestScriptWithoutClassNameUsesFileName(t *testing.T) {
	symbols := analyze(t, "enemy_ai.gd", "extends \"res://actors/base.gd\"\n\nfunc think():\n\tpass\n")
	cls := symbols[0]
	if cls.Name != "enemy_ai" || cls.Signature != `extends "res://actors/base.gd"` {
		t.Fatalf("script class = %+v", cls)
	}
	if !hasRelation(cls, pkgParser.Relation{TargetName: "base", Type: pkgParser.RelInheritance}) {
		t.Errorf("missing inheritance on script path: %v", cls.Relations)
	}
	if got := byQualified(t, symbols, "enemy_ai.think").Type; got != pkgParser.Method {
		t.Errorf("think type = %s", got)
	}
}

func TestScriptMembers(t *testing.T) {
	symbols := analyze(t, "player.gd", playerScript)

	sig := byQualified(t, symbols, "Player.health_changed")
	if sig.Type != "signal" || sig.Signature != "signal health_changed(old_value: int, new_value: int)" {
		t.Errorf("signal = %+v", sig)
	}
	byQualified(t, symbols, "Player.died")

	enum := byQualified(t, symbols, "Player.State")
	if enum.Type != "enum" || strings.Join(enum.Metadata["values"].([]string), ",") != "IDLE,RUN,JUMP" {
		t.Errorf("enum = %+v", enum)
	}
	// An unnamed enum declares class constants.
	if got := byQualified(t, symbols, "Player.A").Type; got != pkgParser.Const {
		t.Errorf("anonymous enum value type = %s", got)
	}

	if got := byQualified(t, symbols, "Player.MAX_SPEED"); got.Type != pkgParser.Const || got.Signature != "const MAX_SPEED := 300.0" {
		t.Errorf("const = %+v", got)
	}

	speed := byQualified(t, symbols, "Player.speed")
	if speed.Type != pkgParser.Var || speed.Metadata["is_exported"] != true || speed.Metadata["type"] != "float" {
		t.Errorf("exported var = %+v", speed)
	}
	weapon := byQualified(t, symbols, "Player.weapon")
	if weapon.Metadata["is_onready"] != true || !hasRelation(weapon, pkgParser.Relation{TargetName: "Weapon", Type: pkgParser.RelUsesType}) {
		t.Errorf("onready var = %+v", weapon)
	}
	hp := byQualified(t, symbols, "Player._hp")
	if hp.IsPublic || hp.Metadata["has_setget"] != true || hp.Signature != "var _hp := 100" {
		t.Errorf("setget var = %+v", hp)
	}
	if !hasRelation(hp, pkgParser.Relation{TargetName: "clampi", Type: pkgParser.RelCalls}) {
		t.Errorf("setter calls not collected: %v", hp.Relations)
	}
	if byQualified(t, symbols, "Player.debug").Metadata["is_exported"] != false {
		t.Error("@export_group marked the following plain var as exported")
	}
	if byQualified(t, symbols, "Player.jump").Metadata["is_exported"] != true {
		t.Error("@export_range var not marked exported")
	}
	if byQualified(t, symbols, "Player.count").Metadata["is_static"] != true {
		t.Error("static var not marked static")
	}

	create := byQualified(t, symbols, "Player.create")
	if create.Signature != "static func create() -> Player" || create.Metadata["is_static"] != true {
		t.Errorf("static func = %+v", create)
	}
}

func TestFunctionRelations(t *testing.T) {
	symbols := analyze(t, "player.gd", playerScript)
	move := byQualified(t, symbols, "Player.move")
	if move.Type != pkgParser.Method || move.Docstring != "Moves the player." {
		t.Fatalf("move = %+v", move)
	}
	if move.Signature != "func move(delta: float, dir := Vector2.ZERO) -> void" {
		t.Errorf("signature = %q", move.Signature)
	}
	for _, want := range []pkgParser.Relation{
		// Bare calls are calls on the script itself.
		{TargetName: "move_and_slide", Type: pkgParser.RelCalls},
		{TargetName: "_take_damage", Type: pkgParser.RelCalls},
		// Receivers that name a class resolve by qualified name.
		{TargetName: "heal", Type: pkgParser.RelCalls, Receiver: "self", TargetQualifiedName: "Player.heal"},
		{TargetName: "move", Type: pkgParser.RelCalls, Receiver: "super", TargetQualifiedName: "CharacterBody2D.move"},
		{TargetName: "helper", Type: pkgParser.RelCalls, Receiver: "Utils", TargetQualifiedName: "Utils.helper"},
		// Other receivers stay unresolved.
		{TargetName: "aim", Type: pkgParser.RelCalls, Receiver: "weapon"},
	} {
		if !hasRelation(move, want) {
			t.Errorf("move missing %+v; got %+v", want, move.Relations)
		}
	}

	create := byQualified(t, symbols, "Player.create")
	if !hasRelation(create, pkgParser.Relation{TargetName: "Player", Type: pkgParser.RelUsesType}) {
		t.Errorf("create missing Player type use: %+v", create.Relations)
	}

	damage := byQualified(t, symbols, "Player._take_damage")
	for _, want := range []pkgParser.Relation{
		{TargetName: "health_changed", Type: pkgParser.RelCalls},
		{TargetName: "died", Type: pkgParser.RelCalls},
		{TargetName: "_on_button_pressed", Type: pkgParser.RelCalls},
	} {
		if !hasRelation(damage, want) {
			t.Errorf("_take_damage missing %+v; got %+v", want, damage.Relations)
		}
	}
}

func TestInnerClass(t *testing.T) {
	symbols := analyze(t, "player.gd", playerScript)
	inner := byQualified(t, symbols, "Player.Inner")
	if inner.Type != pkgParser.Class || inner.Signature != "class Inner extends Node" {
		t.Fatalf("inner = %+v", inner)
	}
	if !hasRelation(inner, pkgParser.Relation{TargetName: "Node", Type: pkgParser.RelInheritance}) {
		t.Errorf("inner missing inheritance: %v", inner.Relations)
	}
	x := byQualified(t, symbols, "Player.Inner.x")
	if x.Docstring != "An inner helper." || x.Metadata["class"] != "Inner" {
		t.Errorf("inner var = %+v", x)
	}
	foo := byQualified(t, symbols, "Player.Inner.foo")
	if !hasRelation(foo, pkgParser.Relation{TargetName: "bar", Type: pkgParser.RelCalls, Receiver: "self", TargetQualifiedName: "Player.Inner.bar"}) {
		t.Errorf("self call in inner class not qualified: %+v", foo.Relations)
	}
}

func TestGodot3Syntax(t *testing.T) {
	src := "tool\nextends KinematicBody2D\n\nexport(int) var speed = 10\nonready var label = $Label\nvar hp = 3 setget set_hp, get_hp\n\n" +
		"func _ready():\n\t$Button.connect(\"pressed\", self, \"_on_pressed\")\n\nfunc set_hp(v):\n\thp = v\n"
	symbols := analyze(t, "old.gd", src)
	if symbols[0].Metadata["is_tool"] != true {
		t.Error("bare tool keyword not detected")
	}
	if byQualified(t, symbols, "old.speed").Metadata["is_exported"] != true {
		t.Error("export var not marked exported")
	}
	if byQualified(t, symbols, "old.label").Metadata["is_onready"] != true {
		t.Error("onready var not marked onready")
	}
	hp := byQualified(t, symbols, "old.hp")
	for _, accessor := range []string{"set_hp", "get_hp"} {
		if !hasRelation(hp, pkgParser.Relation{TargetName: accessor, Type: pkgParser.RelCalls}) {
			t.Errorf("setget missing %s: %v", accessor, hp.Relations)
		}
	}
	ready := byQualified(t, symbols, "old._ready")
	if !hasRelation(ready, pkgParser.Relation{TargetName: "_on_pressed", Type: pkgParser.RelCalls}) {
		t.Errorf("string-form connect handler missing: %v", ready.Relations)
	}
}

const playerScene = `[gd_scene load_steps=3 format=3 uid="uid://abc"]

[ext_resource type="Script" path="res://player.gd" id="1_x"]
[ext_resource type="PackedScene" uid="uid://def" path="res://enemy.tscn" id="2_y"]

[sub_resource type="RectangleShape2D" id="RectangleShape2D_1"]
size = Vector2(10, 20)

[node name="Player" type="CharacterBody2D" groups=["players", "hittable"]]
script = ExtResource("1_x")

[node name="Sprite2D" type="Sprite2D" parent="."]

[node name="Arm" type="Node2D" parent="Sprite2D"]

[node name="Enemy" parent="." instance=ExtResource("2_y")]

[connection signal="died" from="Enemy" to="." method="_on_enemy_died"]
`

func TestScene(t *testing.T) {
	symbols := analyze(t, "player.tscn", playerScene)
	if len(symbols) != 1 {
		t.Fatalf("got %d symbols, want 1", len(symbols))
	}
	scene := symbols[0]
	if scene.Name != "player" || scene.Type != "scene" {
		t.Fatalf("scene = %+v", scene)
	}
	if scene.Signature != "scene player.tscn root=Player (CharacterBody2D)" {
		t.Errorf("signature = %q", scene.Signature)
	}
	for _, line := range []string{
		"Player (CharacterBody2D) script=res://player.gd groups=players,hittable",
		"Sprite2D/Arm (Node2D)",
		"Enemy instance=res://enemy.tscn",
		"Enemy.died -> Player._on_enemy_died()",
	} {
		if !strings.Contains(scene.Content, line) {
			t.Errorf("content missing %q:\n%s", line, scene.Content)
		}
	}
	if strings.Contains(scene.Content, "RectangleShape2D") {
		t.Errorf("sub-resource data leaked into content:\n%s", scene.Content)
	}
	for _, want := range []pkgParser.Relation{
		{TargetName: "player", Type: pkgParser.RelDependency},
		{TargetName: "enemy", Type: pkgParser.RelDependency},
		{TargetName: "_on_enemy_died", Type: pkgParser.RelCalls},
	} {
		if !hasRelation(scene, want) {
			t.Errorf("scene missing %+v; got %+v", want, scene.Relations)
		}
	}
	if scripts := scene.Metadata["scripts"].([]string); len(scripts) != 1 || scripts[0] != "res://player.gd" {
		t.Errorf("scripts = %v", scripts)
	}
}

func TestGodot3Scene(t *testing.T) {
	src := "[gd_scene load_steps=2 format=2]\n\n[ext_resource path=\"res://Main.gd\" type=\"Script\" id=1]\n\n" +
		"[node name=\"Main\" type=\"Node\"]\nscript = ExtResource( 1 )\n"
	scene := analyze(t, "main.tscn", src)[0]
	if !hasRelation(scene, pkgParser.Relation{TargetName: "Main", Type: pkgParser.RelDependency}) {
		t.Errorf("Godot 3 ExtResource( 1 ) not resolved: %+v\n%s", scene.Relations, scene.Content)
	}
}

func TestAnalyzeDirectory(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"a.gd":              "extends Node\nfunc a():\n\tpass\n",
		"level/b.tscn":      "[gd_scene format=3]\n\n[node name=\"B\" type=\"Node\"]\n",
		".godot/cache.gd":   "extends Node\n",
		"notes.txt":         "not godot",
		"level/sub/c.gd":    "extends Node\n",
		"level/sub/readme":  "",
		"addons/plugin.gd":  "@tool\nextends EditorPlugin\n",
		"addons/.hidden.gd": "extends Node\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewAnalyzer().Analyze(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var classes []string
	for _, s := range res.Symbols {
		if s.Type == pkgParser.Class || s.Type == "scene" {
			classes = append(classes, s.Name)
		}
	}
	got := strings.Join(classes, ",")
	for _, want := range []string{"a", "b", "c", "plugin", ".hidden"} {
		if !strings.Contains(","+got+",", ","+want+",") {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "cache") {
		t.Errorf("hidden .godot directory was analyzed: %s", got)
	}
}
