package gdscript

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const player = `@tool
## A player.
## Two lines.
class_name Player extends CharacterBody2D

## Speed.
@export var speed: float = 100.0
@onready var sprite = $Sprite
@export_range(0, 10)
var level := 1
var _hp = 3
const MAX = 5
## Fired on death.
signal died(reason)
enum State { IDLE, RUN }
enum { A, B }

## Moves the player.
func move(delta: float) -> void:
	helper()
	self.helper()
	$Anim.play()
	get_node("x").stop()
	sprite.play()
	connect("died", self, "_on_died")
	died.connect(_on_died)
	emit_signal("died")
	died.emit()
	unknown()

func helper():
	pass

func _on_died():
	pass

func _init():
	pass

class Inner extends Node:
	var v = 1
	func run():
		helper()
		move()
	func helper():
		pass
`

func analyze(t *testing.T, path, root, src string) []parser.Symbol {
	t.Helper()
	syms, err := analyzer{}.Analyze(parser.File{Path: path, Root: root, Source: []byte(src), Language: "gdscript"})
	require.NoError(t, err)
	return syms
}

func byQN(syms []parser.Symbol) map[string]parser.Symbol {
	m := map[string]parser.Symbol{}
	for _, s := range syms {
		m[s.QualifiedName] = s
	}
	return m
}

func targets(s parser.Symbol, kind parser.RelationKind) map[string]string {
	m := map[string]string{}
	for _, r := range s.Relations {
		if r.Kind == kind {
			m[r.TargetName] = r.TargetQualifiedName
		}
	}
	return m
}

func TestScriptSymbols(t *testing.T) {
	syms := analyze(t, "/ws/actors/player.gd", "/ws", player)
	m := byQN(syms)
	const mod = "res://actors/player"
	cases := []struct {
		qn       string
		kind     parser.Kind
		owner    string
		exported bool
		start    int
		end      int
		sig      string
		doc      string
	}{
		{mod + ".Player", parser.KindClass, "", true, 4, 4, "class_name Player extends CharacterBody2D", "A player.\nTwo lines."},
		{mod + ".Player.speed", parser.KindVariable, "Player", true, 7, 7, "@export var speed: float = 100.0", "Speed."},
		{mod + ".Player.sprite", parser.KindVariable, "Player", true, 8, 8, "@onready var sprite = $Sprite", ""},
		{mod + ".Player.level", parser.KindVariable, "Player", true, 10, 10, "var level := 1", ""},
		{mod + ".Player._hp", parser.KindVariable, "Player", false, 11, 11, "var _hp = 3", ""},
		{mod + ".Player.MAX", parser.KindConstant, "Player", true, 12, 12, "const MAX = 5", ""},
		{mod + ".Player.died", parser.KindVariable, "Player", true, 14, 14, "signal died(reason)", "Fired on death."},
		{mod + ".Player.State", parser.KindConstant, "Player", true, 15, 15, "enum State { IDLE, RUN }", ""},
		{mod + ".Player.A", parser.KindConstant, "Player", true, 16, 16, "enum { A, B }", ""},
		{mod + ".Player.move", parser.KindFunction, "Player", true, 19, 29, "func move(delta: float) -> void", "Moves the player."},
		{mod + ".Player.helper", parser.KindFunction, "Player", true, 31, 32, "func helper()", ""},
		{mod + ".Player._on_died", parser.KindFunction, "Player", false, 34, 35, "func _on_died()", ""},
		{mod + ".Player._init", parser.KindFunction, "Player", false, 37, 38, "func _init()", ""},
		{mod + ".Inner", parser.KindClass, "", true, 40, 46, "class Inner extends Node", ""},
		{mod + ".Inner.v", parser.KindVariable, "Inner", true, 41, 41, "var v = 1", ""},
		{mod + ".Inner.run", parser.KindMethod, "Inner", true, 42, 44, "func run()", ""},
		{mod + ".Inner.helper", parser.KindMethod, "Inner", true, 45, 46, "func helper()", ""},
	}
	for _, tc := range cases {
		s, ok := m[tc.qn]
		require.True(t, ok, tc.qn)
		assert.Equal(t, tc.kind, s.Kind, tc.qn)
		assert.Equal(t, tc.owner, s.Owner, tc.qn)
		assert.Equal(t, mod, s.Module, tc.qn)
		assert.Equal(t, tc.exported, s.Exported, tc.qn)
		assert.Equal(t, tc.start, s.StartLine, tc.qn)
		assert.Equal(t, tc.end, s.EndLine, tc.qn)
		assert.Equal(t, tc.sig, s.Signature, tc.qn)
		assert.Equal(t, tc.doc, s.Doc, tc.qn)
		assert.Equal(t, "gdscript", s.Language, tc.qn)
		assert.NotEmpty(t, s.ID, tc.qn)
	}
	assert.Len(t, syms, len(cases)+1) // + enum value B

	assert.Equal(t, map[string]string{"export": "true"}, m[mod+".Player.speed"].Metadata)
	assert.Equal(t, map[string]string{"onready": "true"}, m[mod+".Player.sprite"].Metadata)
	assert.Equal(t, map[string]string{"export": "true"}, m[mod+".Player.level"].Metadata)
	assert.Nil(t, m[mod+".Player._hp"].Metadata)
	assert.Equal(t, map[string]string{"gd": "signal"}, m[mod+".Player.died"].Metadata)
	assert.Equal(t, map[string]string{"gd": "enum"}, m[mod+".Player.State"].Metadata)
	assert.True(t, strings.HasPrefix(m[mod+".Player.move"].Content, "func move(delta: float) -> void:\n\thelper()"))
	assert.Equal(t, "class_name Player extends CharacterBody2D", m[mod+".Player"].Content)
}

func TestScriptRelations(t *testing.T) {
	m := byQN(analyze(t, "/ws/actors/player.gd", "/ws", player))
	const mod = "res://actors/player"
	assert.Equal(t, map[string]string{"CharacterBody2D": ""}, targets(m[mod+".Player"], parser.RelExtends))
	assert.Equal(t, map[string]string{"Node": ""}, targets(m[mod+".Inner"], parser.RelExtends))

	move := m[mod+".Player.move"]
	assert.Equal(t, map[string]string{
		"helper":      mod + ".Player.helper",
		"play":        "",
		"stop":        "",
		"get_node":    "",
		"connect":     "",
		"_on_died":    mod + ".Player._on_died",
		"emit_signal": "",
		"died":        mod + ".Player.died",
		"emit":        "",
		"unknown":     "",
	}, targets(move, parser.RelCall))
	lines := map[string][]int{}
	for _, r := range move.Relations {
		lines[r.TargetName] = append(lines[r.TargetName], r.Line)
	}
	assert.Equal(t, []int{20, 21}, lines["helper"])
	assert.Equal(t, []int{25, 26}, lines["_on_died"])
	assert.Equal(t, []int{27, 28}, lines["died"])

	// Inner classes resolve only against their own members.
	assert.Equal(t, map[string]string{"helper": mod + ".Inner.helper", "move": ""}, targets(m[mod+".Inner.run"], parser.RelCall))
}

func TestExtendsPath(t *testing.T) {
	src := "extends \"res://base/enemy.gd\"\n\nfunc hit():\n\tdie()\n"
	m := byQN(analyze(t, "/ws/mobs/slime.gd", "/ws", src))
	cls, ok := m["res://mobs/slime.slime"]
	require.True(t, ok, "script without class_name gets a class named after the file")
	assert.Equal(t, parser.KindClass, cls.Kind)
	assert.Equal(t, 1, cls.StartLine)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelExtends, TargetName: "enemy", TargetQualifiedName: "res://base/enemy", Line: 1}}, cls.Relations)
	hit := m["res://mobs/slime.hit"]
	assert.Equal(t, parser.KindFunction, hit.Kind)
	assert.Equal(t, "", hit.Owner)
	assert.Equal(t, map[string]string{"die": ""}, targets(hit, parser.RelCall))

	rel := byQN(analyze(t, "/ws/mobs/slime.gd", "/ws", "extends \"enemy.gd\"\n"))
	assert.Equal(t, "res://mobs/enemy", rel["res://mobs/slime.slime"].Relations[0].TargetQualifiedName)
	uid := byQN(analyze(t, "/ws/mobs/slime.gd", "/ws", "extends \"uid://abc\"\n"))
	assert.Equal(t, "", uid["res://mobs/slime.slime"].Relations[0].TargetQualifiedName)
}

func TestGodot3Keywords(t *testing.T) {
	m := byQN(analyze(t, "/ws/a.gd", "/ws", "export var g = 1\nonready var o = $A\n"))
	assert.Equal(t, map[string]string{"export": "true"}, m["res://a.g"].Metadata)
	assert.Equal(t, map[string]string{"onready": "true"}, m["res://a.o"].Metadata)
}

func TestModule(t *testing.T) {
	cases := []struct{ path, root, want string }{
		{"/ws/actors/player.gd", "/ws", "res://actors/player"},
		{"/ws/main.tscn", "/ws", "res://main"},
		{"/elsewhere/x.gd", "/ws", "res://x"},
		{"/ws/x.gd", "", "res://x"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, module(parser.File{Path: tc.path, Root: tc.root}), tc.path)
	}
}

const mainScene = `[gd_scene load_steps=4 format=3 uid="uid://b1"]

[ext_resource type="Script" path="res://actors/player.gd" id="1_p"]
[ext_resource type="PackedScene" path="res://ui/hud.tscn" id="2_h"]
[ext_resource type="Script" path="res://ui/button.gd" id="3_b"]

[node name="Main" type="Node2D" groups=["level", "root"]]
script = ExtResource("1_p")
position = Vector2(1, 2)

[node name="Body" type="CharacterBody2D" parent="."]
collision_layer = 2

[node name="Arm" type="Sprite2D" parent="Body"]

[node name="Hud" parent="." instance=ExtResource("2_h")]

[node name="Start" type="Button" parent="Hud" node_paths=PackedStringArray("a", "b")]
script = ExtResource( 3_b )

[connection signal="pressed" from="Hud/Start" to="." method="_on_start_pressed"]
[connection signal="body_entered" from="Body" to="Hud/Start" method="_on_body"]
[connection signal="ready" from="Nope" to="." method="_x"]
`

func TestScene(t *testing.T) {
	syms := analyze(t, "/ws/levels/main.tscn", "/ws", mainScene)
	m := byQN(syms)
	const mod = "res://levels/main"
	cases := []struct {
		qn, owner, sig string
		start, end     int
		meta           map[string]string
	}{
		{mod + ".Main", "", "Node2D", 7, 9, map[string]string{"type": "Node2D"}},
		{mod + ".Main.Body", "Main", "CharacterBody2D", 11, 12, map[string]string{"type": "CharacterBody2D", "parent": "."}},
		{mod + ".Main/Body.Arm", "Main/Body", "Sprite2D", 14, 14, map[string]string{"type": "Sprite2D", "parent": "Body"}},
		{mod + ".Main.Hud", "Main", "", 16, 16, map[string]string{"type": "", "parent": ".", "instance": "res://ui/hud.tscn"}},
		{mod + ".Main/Hud.Start", "Main/Hud", "Button", 18, 19, map[string]string{"type": "Button", "parent": "Hud"}},
	}
	require.Len(t, syms, len(cases))
	for _, tc := range cases {
		s, ok := m[tc.qn]
		require.True(t, ok, tc.qn)
		assert.Equal(t, parser.KindScene, s.Kind, tc.qn)
		assert.Equal(t, tc.owner, s.Owner, tc.qn)
		assert.Equal(t, tc.sig, s.Signature, tc.qn)
		assert.Equal(t, tc.start, s.StartLine, tc.qn)
		assert.Equal(t, tc.end, s.EndLine, tc.qn)
		assert.Equal(t, tc.meta, s.Metadata, tc.qn)
		assert.True(t, s.Exported, tc.qn)
	}
	assert.Equal(t, "[node name=\"Main\" type=\"Node2D\" groups=[\"level\", \"root\"]]\nscript = ExtResource(\"1_p\")\nposition = Vector2(1, 2)", m[mod+".Main"].Content)

	assert.Equal(t, []parser.Relation{
		{Kind: parser.RelUsesType, TargetName: "player", TargetQualifiedName: "res://actors/player", Line: 8},
	}, m[mod+".Main"].Relations)
	assert.Equal(t, []parser.Relation{
		{Kind: parser.RelUsesType, TargetName: "button", TargetQualifiedName: "res://ui/button", Line: 19},
		{Kind: parser.RelCall, TargetName: "_on_start_pressed", TargetQualifiedName: "res://actors/player._on_start_pressed", Line: 21},
	}, m[mod+".Main/Hud.Start"].Relations)
	assert.Equal(t, []parser.Relation{
		{Kind: parser.RelCall, TargetName: "_on_body", TargetQualifiedName: "res://ui/button._on_body", Line: 22},
	}, m[mod+".Main.Body"].Relations)
}

func TestSceneCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("[gd_scene format=3]\n[node name=\"Root\" type=\"Node\"]\n")
	for i := 0; i < maxNodes+10; i++ {
		fmt.Fprintf(&b, "[node name=\"N%d\" type=\"Node\" parent=\".\"]\n", i)
	}
	fmt.Fprintf(&b, "[connection signal=\"s\" from=\"N%d\" to=\".\" method=\"m\"]\n", maxNodes+5)
	syms := analyze(t, "/ws/big.tscn", "/ws", b.String())
	require.Len(t, syms, maxNodes)
	assert.Equal(t, "true", syms[maxNodes-1].Metadata["truncated"])
	assert.Empty(t, syms[maxNodes-2].Metadata["truncated"])

	long := "[node name=\"R\" type=\"Node\"]\ndata = " + strings.Repeat("1,", maxContent) + "\n"
	syms = analyze(t, "/ws/long.tscn", "/ws", long)
	require.Len(t, syms, 1)
	assert.Len(t, syms[0].Content, maxContent)
}

func TestSceneGarbage(t *testing.T) {
	for _, src := range []string{"", "\x00\xff[[[", "[node", "[node name=]\n[connection from=\".\"]\n", "script = ExtResource(\"1\")\n[ext_resource type=\"Script\" id=\"1\"]"} {
		assert.Empty(t, analyze(t, "/ws/g.tscn", "/ws", src), src)
	}
	syms := analyze(t, "/ws/g.tscn", "/ws", "[node name=\"A\" type=\"Node\" parent=\"Missing\"]\nscript = ExtResource(\"9\")\n[connection signal=\"s\" from=\".\" method=\"m\"]\n")
	require.Len(t, syms, 1)
	assert.Equal(t, "res://g.Missing.A", syms[0].QualifiedName)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelCall, TargetName: "m", Line: 3}}, syms[0].Relations)
}

func TestMalformedScript(t *testing.T) {
	src := "class_name Ok\n\nfunc good():\n\tpass\n\nfunc broken(:\n\tif x\n\t\tfoo(\n"
	syms := analyze(t, "/ws/m.gd", "/ws", src)
	m := byQN(syms)
	require.Contains(t, m, "res://m.Ok")
	good, ok := m["res://m.Ok.good"]
	require.True(t, ok)
	assert.Equal(t, 3, good.StartLine)
	for _, s := range syms {
		assert.NotEmpty(t, s.QualifiedName)
		assert.LessOrEqual(t, s.StartLine, s.EndLine)
	}
	assert.Empty(t, analyze(t, "/ws/e.gd", "/ws", ""))
	for _, s := range analyze(t, "/ws/g.gd", "/ws", "\x00\xff(((") {
		assert.NotEmpty(t, s.QualifiedName)
	}
}

func TestRegistered(t *testing.T) {
	for _, p := range []string{"/ws/x.gd", "/ws/x.GD", "/ws/x.tscn"} {
		a := parser.ForFile(p)
		require.NotNil(t, a, p)
		assert.Equal(t, "gdscript", a.Language())
	}
	syms, err := parser.Analyze(parser.File{Path: "/ws/x.gd", Root: "/ws", Source: []byte("func f():\n\tpass\n")})
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "gdscript", syms[0].Language)
	assert.Equal(t, "res://x.f", syms[0].QualifiedName)
}

func TestConcurrent(t *testing.T) {
	wantGD := analyze(t, "/ws/actors/player.gd", "/ws", player)
	wantScene := analyze(t, "/ws/levels/main.tscn", "/ws", mainScene)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				assert.Equal(t, wantGD, analyze(t, "/ws/actors/player.gd", "/ws", player))
				assert.Equal(t, wantScene, analyze(t, "/ws/levels/main.tscn", "/ws", mainScene))
			}
		}()
	}
	wg.Wait()
}

// super.m() and super() target the base class's method when the base is a
// class_name (a qualified suffix the index resolves); with a path base they
// stay by name. A bare super() is not a call to "super".
func TestSuperCalls(t *testing.T) {
	src := "class_name Door\nextends AreaTransition\nfunc _ready():\n\tsuper._ready()\n\tsuper()\n\tawait super._on_area_entered(a)\n"
	m := byQN(analyze(t, "/ws/door.gd", "/ws", src))
	got := map[string]string{}
	for _, r := range m["res://door.Door._ready"].Relations {
		got[r.TargetName] = r.TargetQualifiedName
	}
	assert.Equal(t, map[string]string{"_ready": "AreaTransition._ready", "_on_area_entered": "AreaTransition._on_area_entered"}, got)

	src = "extends \"res://base.gd\"\nfunc _ready():\n\tsuper._ready()\n"
	m = byQN(analyze(t, "/ws/x.gd", "/ws", src))
	var rels []parser.Relation
	for _, s := range m {
		if s.Name == "_ready" {
			rels = s.Relations
		}
	}
	require.Len(t, rels, 1)
	assert.Equal(t, "_ready", rels[0].TargetName)
	assert.Empty(t, rels[0].TargetQualifiedName)
}
