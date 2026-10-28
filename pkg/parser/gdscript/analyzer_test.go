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
	if !hasRelation(hp, pkgParser.Relation{TargetName: "clampi", Type: pkgParser.RelCalls, TargetQualifiedName: "CharacterBody2D.clampi"}) {
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
		// Bare calls target the script when it declares the method, else
		// its base class; never a same-named method elsewhere.
		{TargetName: "move_and_slide", Type: pkgParser.RelCalls, TargetQualifiedName: "CharacterBody2D.move_and_slide"},
		{TargetName: "_take_damage", Type: pkgParser.RelCalls, TargetQualifiedName: "Player._take_damage"},
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
		{TargetName: "health_changed", Type: pkgParser.RelCalls, TargetQualifiedName: "Player.health_changed"},
		{TargetName: "died", Type: pkgParser.RelCalls, TargetQualifiedName: "Player.died"},
		{TargetName: "_on_button_pressed", Type: pkgParser.RelCalls, TargetQualifiedName: "Player._on_button_pressed"},
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
	for accessor, target := range map[string]string{"set_hp": "old.set_hp", "get_hp": "KinematicBody2D.get_hp"} {
		if !hasRelation(hp, pkgParser.Relation{TargetName: accessor, Type: pkgParser.RelCalls, TargetQualifiedName: target}) {
			t.Errorf("setget missing %s: %v", accessor, hp.Relations)
		}
	}
	ready := byQualified(t, symbols, "old._ready")
	if !hasRelation(ready, pkgParser.Relation{TargetName: "_on_pressed", Type: pkgParser.RelCalls, Receiver: "self", TargetQualifiedName: "KinematicBody2D._on_pressed"}) {
		t.Errorf("string-form connect handler missing: %v", ready.Relations)
	}
}

// Calls on node paths, get_node() results or other expressions are member
// calls: they keep their receiver and must not resolve to a same-named method
// of the script itself.
func TestNonIdentifierReceiversStayMemberCalls(t *testing.T) {
	src := "extends Node\n\nfunc play():\n\t$AnimationPlayer.play(\"run\")\n\tget_node(\"Sfx\").stop()\n\titems[0].use()\n\tget_tree().create_timer(1.0)\n\n" +
		"func stop():\n\tpass\n\nfunc use():\n\tpass\n"
	play := byQualified(t, analyze(t, "music.gd", src), "music.play")
	for _, want := range []pkgParser.Relation{
		{TargetName: "play", Type: pkgParser.RelCalls, Receiver: "$AnimationPlayer"},
		{TargetName: "stop", Type: pkgParser.RelCalls, Receiver: `get_node("Sfx")`},
		{TargetName: "use", Type: pkgParser.RelCalls, Receiver: "items[0]"},
		{TargetName: "create_timer", Type: pkgParser.RelCalls, Receiver: "get_tree()"},
	} {
		if !hasRelation(play, want) {
			t.Errorf("missing %+v; got %+v", want, play.Relations)
		}
	}
}

// A bare call inside the script must not target a same-named method of an
// inner class, and an inner class's bare call must not target the script.
func TestBareCallsStayInTheirClass(t *testing.T) {
	src := "extends Node2D\n\nfunc _process(delta):\n\tupdate()\n\nfunc helper():\n\tpass\n\n" +
		"class Item:\n\tfunc update():\n\t\tpass\n\nclass Other extends Item:\n\tfunc go():\n\t\thelper()\n\t\tupdate()\n"
	symbols := analyze(t, "outer.gd", src)
	process := byQualified(t, symbols, "outer._process")
	if !hasRelation(process, pkgParser.Relation{TargetName: "update", Type: pkgParser.RelCalls, TargetQualifiedName: "Node2D.update"}) {
		t.Errorf("_process update(): %+v", process.Relations)
	}
	goSym := byQualified(t, symbols, "outer.Other.go")
	for _, want := range []pkgParser.Relation{
		// Other does not declare helper; its base Item does not either.
		{TargetName: "helper", Type: pkgParser.RelCalls, TargetQualifiedName: "outer.Item.helper"},
		// Inherited from the inner base class.
		{TargetName: "update", Type: pkgParser.RelCalls, TargetQualifiedName: "outer.Item.update"},
	} {
		if !hasRelation(goSym, want) {
			t.Errorf("go missing %+v; got %+v", want, goSym.Relations)
		}
	}
}

func TestReceiverClassChains(t *testing.T) {
	src := "class_name Foo\nextends Node\n\nfunc run():\n\tInner.make()\n\tInner.Deep.go()\n\tOtherClass.Sub.go()\n\tVector2.from_angle(1.0)\n\n" +
		"class Inner:\n\tstatic func make():\n\t\tpass\n\tclass Deep:\n\t\tstatic func go():\n\t\t\tpass\n"
	run := byQualified(t, analyze(t, "foo.gd", src), "Foo.run")
	for _, want := range []pkgParser.Relation{
		{TargetName: "make", Type: pkgParser.RelCalls, Receiver: "Inner", TargetQualifiedName: "Foo.Inner.make"},
		{TargetName: "go", Type: pkgParser.RelCalls, Receiver: "Inner.Deep", TargetQualifiedName: "Foo.Inner.Deep.go"},
		{TargetName: "go", Type: pkgParser.RelCalls, Receiver: "OtherClass.Sub", TargetQualifiedName: "OtherClass.Sub.go"},
		{TargetName: "from_angle", Type: pkgParser.RelCalls, Receiver: "Vector2"},
	} {
		if !hasRelation(run, want) {
			t.Errorf("missing %+v; got %+v", want, run.Relations)
		}
	}
}

func TestBareConnectRecordsHandler(t *testing.T) {
	src := "extends Node\n\nfunc _ready():\n\tconnect(\"died\", self, \"_on_died\")\n\tconnect(\"hit\", _on_hit)\n\n" +
		"func _on_died():\n\tpass\n\nfunc _on_hit():\n\tpass\n"
	ready := byQualified(t, analyze(t, "e.gd", src), "e._ready")
	for _, handler := range []string{"_on_died", "_on_hit"} {
		receiver := ""
		if handler == "_on_died" {
			receiver = "self"
		}
		if !hasRelation(ready, pkgParser.Relation{TargetName: handler, Type: pkgParser.RelCalls, Receiver: receiver, TargetQualifiedName: "e." + handler}) {
			t.Errorf("missing handler %s: %+v", handler, ready.Relations)
		}
	}
}

func TestDocComments(t *testing.T) {
	src := "extends Node\r\n## Class doc.\r\n\r\n## The speed.\r\n@export_range(0, 10)\r\nvar speed := 1\r\n" +
		"var a = 1 ## Inline doc for a.\r\nvar b = 2\r\n\r\n## Networked.\r\n@rpc(\"any_peer\")\r\nfunc net():\r\n\tpass\r\n" +
		"\r\nsignal hit(\r\n\tamount: int,\r\n\tsource: Node\r\n)\r\n"
	symbols := analyze(t, "docs.gd", src)
	if got := symbols[0].Docstring; got != "Class doc." {
		t.Errorf("class doc = %q", got)
	}
	for qn, want := range map[string]string{
		"docs.speed": "The speed.",
		"docs.a":     "Inline doc for a.",
		"docs.b":     "",
		"docs.net":   "Networked.",
	} {
		if got := byQualified(t, symbols, qn).Docstring; got != want {
			t.Errorf("%s doc = %q, want %q", qn, got, want)
		}
	}
	if got := byQualified(t, symbols, "docs.net").Metadata["annotations"].([]string); len(got) != 1 || got[0] != "rpc" {
		t.Errorf("net annotations = %v", got)
	}
	if got := byQualified(t, symbols, "docs.hit").Signature; got != "signal hit(amount: int, source: Node)" {
		t.Errorf("multi-line signal signature = %q", got)
	}
}

func TestAnnotationOwnership(t *testing.T) {
	src := "@tool\n@abstract\nclass_name Shape\nextends Node\n\n@abstract\nfunc area() -> float:\n\treturn 0.0\n\n@export_range(0, 10)\nvar x = 1\n"
	symbols := analyze(t, "shape.gd", src)
	if got := strings.Join(symbols[0].Metadata["annotations"].([]string), ","); got != "tool,abstract" {
		t.Errorf("script annotations = %s", got)
	}
	if got := strings.Join(byQualified(t, symbols, "Shape.area").Metadata["annotations"].([]string), ","); got != "abstract" {
		t.Errorf("area annotations = %s", got)
	}
	if got := strings.Join(byQualified(t, symbols, "Shape.x").Metadata["annotations"].([]string), ","); got != "export_range" {
		t.Errorf("x annotations = %s", got)
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
	if scene.Name != "player.tscn" || scene.QualifiedName != "player.tscn" || scene.Type != "scene" || scene.Package != "player" {
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
		// No project.godot: scripts are known by file name only.
		{TargetName: "player", Type: pkgParser.RelDependency},
		{TargetName: "enemy.tscn", Type: pkgParser.RelDependency},
		// The handler belongs to the script on the target node (the root).
		{TargetName: "_on_enemy_died", Type: pkgParser.RelCalls, TargetQualifiedName: "player._on_enemy_died"},
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
	for _, want := range []string{"a", "b.tscn", "c", "plugin", ".hidden"} {
		if !strings.Contains(","+got+",", ","+want+",") {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "cache") {
		t.Errorf("hidden .godot directory was analyzed: %s", got)
	}
}

// Inside a project, a scene names attached scripts by their class_name, and a
// connection targets the handler of the script on the target node, so a
// handler name shared by several scripts still resolves to the right one.
func TestSceneResolvesScriptClassNames(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"project.godot":    "config_version=5\n",
		"actors/player.gd": "@tool\nclass_name Player\nextends CharacterBody2D\n",
		"ui/hud.gd":        "extends Control\n",
		"levels/level.tscn": `[gd_scene load_steps=3 format=3]

[ext_resource type="Script" path="res://actors/player.gd" id="1"]
[ext_resource type="Script" path="res://ui/hud.gd" id="2"]

[node name="Level" type="Node2D"]

[node name="Player" type="CharacterBody2D" parent="."]
script = ExtResource("1")

[node name="HUD" type="Control" parent="."]
script = ExtResource("2")

[node name="Timer" type="Timer" parent="."]

[connection signal="timeout" from="Timer" to="Player" method="_on_timer_timeout"]
[connection signal="timeout" from="Timer" to="HUD" method="_on_timer_timeout"]
[connection signal="ready" from="." to="." method="_on_ready"]
`,
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewAnalyzer().Analyze(context.Background(), filepath.Join(root, "levels", "level.tscn"))
	if err != nil {
		t.Fatal(err)
	}
	scene := res.Symbols[0]
	for _, want := range []pkgParser.Relation{
		{TargetName: "Player", Type: pkgParser.RelDependency},
		{TargetName: "hud", Type: pkgParser.RelDependency},
		{TargetName: "_on_timer_timeout", Type: pkgParser.RelCalls, TargetQualifiedName: "Player._on_timer_timeout"},
		{TargetName: "_on_timer_timeout", Type: pkgParser.RelCalls, TargetQualifiedName: "hud._on_timer_timeout"},
		// The root has no script in this scene: name-only.
		{TargetName: "_on_ready", Type: pkgParser.RelCalls},
	} {
		if !hasRelation(scene, want) {
			t.Errorf("missing %+v; got %+v", want, scene.Relations)
		}
	}
}

// Embedded resource data is serialized as one huge line; it must not reach
// the parser (it overflowed the stack on large scenes).
func TestSceneWithLargeEmbeddedData(t *testing.T) {
	var data strings.Builder
	data.WriteString("[gd_scene load_steps=2 format=3]\n\n[sub_resource type=\"ArrayMesh\" id=\"1\"]\n_surfaces = [{\n\"vertex_data\": PackedByteArray(")
	for i := 0; i < 1_000_000; i++ {
		data.WriteString("255, ")
	}
	data.WriteString("0)\n}]\n\n[node name=\"Root\" type=\"Node3D\"]\n\n[node name=\"Mesh\" type=\"MeshInstance3D\" parent=\".\"]\nmesh = SubResource(\"1\")\n")
	symbols := analyze(t, "big.tscn", data.String())
	if len(symbols) != 1 || symbols[0].Metadata["root_node"] != "Root" || symbols[0].EndLine != 12 {
		t.Fatalf("symbols = %+v", symbols)
	}
	if !strings.Contains(symbols[0].Content, "Mesh (MeshInstance3D)") {
		t.Errorf("content = %s", symbols[0].Content)
	}
}
