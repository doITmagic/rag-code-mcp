package gdscript

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// Independent verification probes (not written by the implementer).

const verifyEnemy = `extends "res://actors/base_enemy.gd"
class_name Enemy

signal hurt(amount)
@export var hp := 10
static var count := 0

static func spawn() -> Enemy:
	count += 1
	return Enemy.new()

func take_damage(n: int) -> void:
	hp -= n
	emit_signal("hurt", n)
	hurt.emit(n)
	_die()
	self._die()
	$Sprite.hide()
	get_node("Anim").play("hit")
	owner.notify()
	Enemy.spawn()
	missing()
	timer.connect("timeout", other, "on_timeout")
	connect("hurt", self, "_on_hurt")
	hurt.connect(_on_hurt)
	Loot.drop()

func _die():
	queue_free()

func _on_hurt(n):
	pass

func on_timeout():
	pass

class Loot extends RefCounted:
	signal dropped
	static func drop():
		emit_signal("dropped")
		take_damage(1)
`

func TestVerifyScript(t *testing.T) {
	syms := analyze(t, "/ws/actors/enemy.gd", "/ws", verifyEnemy)
	m := byQN(syms)
	const mod = "res://actors/enemy"

	cls, ok := m[mod+".Enemy"]
	require.True(t, ok, "class_name on line 2 after extends on line 1")
	assert.Equal(t, parser.KindClass, cls.Kind)
	assert.Equal(t, 2, cls.StartLine)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelExtends, TargetName: "base_enemy", TargetQualifiedName: "res://actors/base_enemy", Line: 1}}, cls.Relations)
	_, dup := m[mod+".enemy"]
	assert.False(t, dup, "no synthetic file class when class_name is declared")

	hurt := m[mod+".Enemy.hurt"]
	assert.Equal(t, parser.KindVariable, hurt.Kind)
	assert.Equal(t, map[string]string{"gd": "signal"}, hurt.Metadata)
	assert.Equal(t, map[string]string{"export": "true"}, m[mod+".Enemy.hp"].Metadata)
	assert.Equal(t, parser.KindVariable, m[mod+".Enemy.count"].Kind)

	spawn := m[mod+".Enemy.spawn"]
	assert.Equal(t, parser.KindFunction, spawn.Kind)
	assert.Equal(t, "Enemy", spawn.Owner)
	assert.Equal(t, "static func spawn() -> Enemy", spawn.Signature)
	assert.True(t, strings.HasPrefix(spawn.Content, "static func spawn()"))
	assert.Equal(t, 8, spawn.StartLine)
	assert.Equal(t, 10, spawn.EndLine)

	td := m[mod+".Enemy.take_damage"]
	assert.Equal(t, map[string]string{
		"emit_signal": "",
		"hurt":        mod + ".Enemy.hurt",
		"emit":        "",
		"_die":        mod + ".Enemy._die",
		"hide":        "",
		"get_node":    "",
		"play":        "",
		"notify":      "",
		"spawn":       "",
		"missing":     "",
		"connect":     "",
		"_on_hurt":    mod + ".Enemy._on_hurt",
		"on_timeout":  "",
		"drop":        "",
	}, targets(td, parser.RelCall))
	for _, r := range td.Relations {
		assert.GreaterOrEqual(t, r.Line, 13, r.TargetName)
		assert.LessOrEqual(t, r.Line, 27, r.TargetName)
	}

	assert.False(t, m[mod+".Enemy._die"].Exported)
	assert.Equal(t, "Enemy", m[mod+".Enemy._die"].Owner)

	loot := m[mod+".Loot"]
	assert.Equal(t, parser.KindClass, loot.Kind)
	assert.Equal(t, "", loot.Owner)
	assert.Equal(t, map[string]string{"RefCounted": ""}, targets(loot, parser.RelExtends))
	drop := m[mod+".Loot.drop"]
	assert.Equal(t, parser.KindMethod, drop.Kind)
	assert.Equal(t, "Loot", drop.Owner)
	assert.Equal(t, map[string]string{"emit_signal": "", "dropped": mod + ".Loot.dropped", "take_damage": ""}, targets(drop, parser.RelCall))

	for _, s := range syms {
		assert.Equal(t, parser.Qualify(s.Module, s.Owner, s.Name), s.QualifiedName)
		assert.Equal(t, "/ws/actors/enemy.gd", s.FilePath)
		assert.NotEmpty(t, s.Content, s.QualifiedName)
	}
}

const verifyG3Scene = `[gd_scene load_steps=3 format=2]

[ext_resource path="res://actors/enemy.gd" type="Script" id=1]
[ext_resource path="res://ui/hud.gd" type="Script" id=2]

[node name="Level" type="Node2D"]
script = ExtResource( 1 )

[node name="UI" type="CanvasLayer" parent="."]

[node name="Hud" type="Control" parent="UI"]
script = ExtResource( 2 )

[node name="Label" type="Label" parent="UI/Hud"]
text = "hi"

[connection signal="hurt" from="." to="UI/Hud" method="_on_enemy_hurt"]
[connection signal="resized" from="UI/Hud/Label" to="." method="_on_label_resized"]
`

func TestVerifyScene(t *testing.T) {
	syms := analyze(t, "/ws/levels/l1.tscn", "/ws", verifyG3Scene)
	m := byQN(syms)
	const mod = "res://levels/l1"
	require.Len(t, syms, 4)
	root := m[mod+".Level"]
	assert.Equal(t, parser.KindScene, root.Kind)
	assert.Equal(t, "Node2D", root.Signature)
	assert.Equal(t, 6, root.StartLine)
	assert.Equal(t, 7, root.EndLine)
	assert.Equal(t, []parser.Relation{
		{Kind: parser.RelUsesType, TargetName: "enemy", TargetQualifiedName: "res://actors/enemy", Line: 7},
		{Kind: parser.RelCall, TargetName: "_on_enemy_hurt", TargetQualifiedName: "res://ui/hud._on_enemy_hurt", Line: 17},
	}, root.Relations)
	hud := m[mod+".Level/UI.Hud"]
	require.NotEmpty(t, hud.Name)
	assert.Equal(t, "UI", hud.Metadata["parent"])
	assert.Equal(t, []parser.Relation{{Kind: parser.RelUsesType, TargetName: "hud", TargetQualifiedName: "res://ui/hud", Line: 12}}, hud.Relations)
	label := m[mod+".Level/UI/Hud.Label"]
	require.NotEmpty(t, label.Name)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelCall, TargetName: "_on_label_resized", TargetQualifiedName: "res://actors/enemy._on_label_resized", Line: 18}}, label.Relations)
	for _, s := range syms {
		assert.Equal(t, parser.Qualify(s.Module, s.Owner, s.Name), s.QualifiedName)
		assert.Equal(t, "", s.Metadata["truncated"])
	}
}

func TestVerifyRobust(t *testing.T) {
	inputs := map[string]string{
		"empty":   "",
		"one":     "f",
		"newline": "\n",
		"binary":  "\x00\x01\xff\xfe\x80[node\x00name=\"\xff\"]\nscript = ExtResource(\n",
		"trunc":   "class_name A\nfunc f(\n\tif",
		"deep":    "func f():\n\tx = " + strings.Repeat("(", 5000) + strings.Repeat(")", 5000) + "\n",
		"header":  "[node name=\"A\"",
		"quote":   "[node name=\"A\\\"] parent=\"",
		"crlf":    "class_name A\r\nfunc f():\r\n\tg()\r\n",
	}
	for name, src := range inputs {
		for _, p := range []string{"/ws/x.gd", "/ws/x.tscn", "x.gd"} {
			start := time.Now()
			syms, err := analyzer{}.Analyze(parser.File{Path: p, Root: "/ws", Source: []byte(src), Language: "gdscript"})
			assert.NoError(t, err, name+" "+p)
			assert.Less(t, time.Since(start), 5*time.Second, name+" "+p)
			for _, s := range syms {
				assert.NotEmpty(t, s.QualifiedName, name+" "+p)
				assert.LessOrEqual(t, s.StartLine, s.EndLine, name+" "+p)
			}
		}
	}
}

// Second verification round: relative extends, Godot 4 call forms, nested
// inner classes, CRLF scenes, upper-case extensions and large inputs.

const verifyG4 = `extends "../base/actor.gd"

const Enemy = preload("res://actors/enemy.gd")
@export var items: Array[int] = []

func _ready() -> void:
	super()
	super._ready()
	%Unique.show()
	var cb = func(): helper(1)
	await helper(2)
	Enemy.spawn()
	pressed.connect(_on_pressed.bind(1))
	connect(&"pressed", _on_pressed)
	timer.connect("timeout", self, "_on_timeout")
	self.emit_signal(&"pressed")

signal pressed

func helper(n): pass
func _on_pressed(): pass
func _on_timeout(): pass

class Outer:
	class Deep:
		func d():
			e()
		func e(): pass
	func o():
		pass
`

func TestVerifyGodot4(t *testing.T) {
	syms := analyze(t, "/ws/actors/x.gd", "/ws", verifyG4)
	m := byQN(syms)
	const mod = "res://actors/x"

	// A relative extends path is resolved against the script's directory and
	// must be a clean res:// module, or the indexer never matches it.
	cls := m[mod+".x"]
	require.Len(t, cls.Relations, 1)
	assert.Equal(t, "res://base/actor", cls.Relations[0].TargetQualifiedName)

	assert.Equal(t, parser.KindConstant, m[mod+".Enemy"].Kind)
	assert.Equal(t, map[string]string{"export": "true"}, m[mod+".items"].Metadata)

	ready := m[mod+".x._ready"]
	if ready.Name == "" {
		ready = m[mod+"._ready"]
	}
	require.NotEmpty(t, ready.Name)
	assert.Equal(t, "", ready.Owner, "no class_name: free function")
	assert.Equal(t, map[string]string{
		"_ready":      "", // super._ready() belongs to the base class
		"show":        "", // %Unique.show()
		"helper":      mod + ".helper",
		"spawn":       "", // Enemy.spawn() through a preload constant
		"connect":     "",
		"bind":        "",
		"_on_pressed": mod + "._on_pressed",
		"_on_timeout": mod + "._on_timeout",
		"emit_signal": "",
		"pressed":     mod + ".pressed",
	}, targets(ready, parser.RelCall))
	lines := map[string]int{}
	for _, r := range ready.Relations {
		lines[r.TargetName] = r.Line
	}
	assert.Equal(t, 15, lines["_on_timeout"])
	assert.Equal(t, 16, lines["pressed"])

	deep := m[mod+".Deep.d"]
	assert.Equal(t, parser.KindMethod, deep.Kind)
	assert.Equal(t, map[string]string{"e": mod + ".Deep.e"}, targets(deep, parser.RelCall))
	assert.Equal(t, parser.KindMethod, m[mod+".Outer.o"].Kind)
	assert.Equal(t, parser.KindClass, m[mod+".Outer"].Kind)
}

func TestVerifySceneCRLFUpper(t *testing.T) {
	src := "[gd_scene format=3]\r\n\r\n[ext_resource type=\"Script\" uid=\"uid://x\" path=\"res://a.gd\" id=\"1_a\"]\r\n\r\n[node name=\"Root Node\" type=\"Node\"]\r\nscript = ExtResource(\"1_a\")\r\n\r\n[node name=\"Kid\" parent=\".\" type=\"Node\"]\r\n\r\n[connection signal=\"s\" from=\"Kid\" to=\".\" method=\"m\"]\r\n"
	syms := analyze(t, "/ws/s.TSCN", "/ws", src)
	m := byQN(syms)
	require.Len(t, syms, 2)
	root := m["res://s.Root Node"]
	assert.Equal(t, []parser.Relation{{Kind: parser.RelUsesType, TargetName: "a", TargetQualifiedName: "res://a", Line: 6}}, root.Relations)
	assert.Equal(t, 5, root.StartLine)
	assert.Equal(t, 6, root.EndLine)
	kid := m["res://s.Root Node.Kid"]
	assert.Equal(t, "Root Node", kid.Owner)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelCall, TargetName: "m", TargetQualifiedName: "res://a.m", Line: 10}}, kid.Relations)
}

func TestVerifyLarge(t *testing.T) {
	var b strings.Builder
	b.WriteString("class_name Big\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "## doc %d\nfunc f%d(a, b):\n\tif a:\n\t\tf%d(b, a)\n\treturn $N.m(\"%d\")\n", i, i, (i+1)%3000, i)
	}
	nested := "func n():\n" + strings.Repeat("\tif true:\n", 300)
	for i := 0; i < 300; i++ {
		nested += strings.Repeat("\t", 301-i)
	}
	for name, src := range map[string]string{"big": b.String(), "nested": nested + "pass\n", "junk": strings.Repeat("\xff\x00[(\"\n", 1000)} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			syms := analyze(t, "/ws/big.gd", "/ws", src)
			assert.Less(t, time.Since(start), 5*time.Second, name)
			if name == "big" {
				require.Len(t, syms, 3001)
				f := syms[1500]
				assert.Equal(t, "Big", f.Owner)
				assert.Equal(t, "doc 1499", f.Doc)
				assert.Equal(t, "res://big.Big.f1500", targets(f, parser.RelCall)["f1500"])
			}
		})
	}
}

// Third verification round: docs, enums, annotations, forward references,
// nested control flow, scripts without class_name, instanced scene nodes.

const verifyRound3 = `## Base shooter.
##
## Second paragraph.
extends Node2D

## Current state.
enum State { IDLE, RUN }
enum { A, B = 4 }
@export_group("Stats")
var plain = 1 # trailing comment
## Max health.
const MAX_HP: int = 100
@onready var anim = $Anim
@export_range(0, 10) var level := 1

## Shoots.
func shoot(dir: Vector2) -> void:
	for i in 3:
		match dir.x:
			0:
				reload()
			_:
				if i > 1:
					_aim(dir)
	var cb := func(): reload()
	reload.call()
	self.emit_signal("fired")
	other.emit_signal("fired")
	self.reload()
	State.keys()

## Fired.
signal fired

func reload(): pass
func _aim(d): pass

class _Hidden:
	## Make one.
	func _init():
		setup()
	func setup(): pass
`

func TestVerifyRound3Script(t *testing.T) {
	syms := analyze(t, "/ws/weapons/gun.gd", "/ws/", verifyRound3)
	m := byQN(syms)
	const mod = "res://weapons/gun"

	cls, ok := m[mod+".gun"]
	require.True(t, ok, "synthetic file class at the extends line")
	assert.Equal(t, 4, cls.StartLine)
	assert.Equal(t, "Base shooter.\n\nSecond paragraph.", cls.Doc)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelExtends, TargetName: "Node2D", Line: 4}}, cls.Relations)

	st := m[mod+".State"]
	assert.Equal(t, parser.KindConstant, st.Kind)
	assert.Equal(t, "Current state.", st.Doc)
	assert.Equal(t, "enum", st.Metadata["gd"])
	assert.Equal(t, "", st.Owner)
	assert.Equal(t, parser.KindConstant, m[mod+".A"].Kind)
	assert.Equal(t, 8, m[mod+".B"].StartLine)
	_, hasIdle := m[mod+".IDLE"]
	assert.False(t, hasIdle, "named enum values are not separate symbols")

	assert.Nil(t, m[mod+".plain"].Metadata, "export_group alone is not an export")
	assert.Equal(t, "Max health.", m[mod+".MAX_HP"].Doc, "trailing comment on the previous line must not shadow the doc")
	assert.Equal(t, parser.KindConstant, m[mod+".MAX_HP"].Kind)
	assert.Equal(t, map[string]string{"onready": "true"}, m[mod+".anim"].Metadata)
	assert.Equal(t, map[string]string{"export": "true"}, m[mod+".level"].Metadata)

	shoot := m[mod+".shoot"]
	require.NotEmpty(t, shoot.Name)
	assert.Equal(t, parser.KindFunction, shoot.Kind)
	assert.Equal(t, "", shoot.Owner)
	assert.Equal(t, "Shoots.", shoot.Doc)
	assert.Equal(t, "func shoot(dir: Vector2) -> void", shoot.Signature)
	assert.Equal(t, 17, shoot.StartLine)
	assert.Equal(t, 30, shoot.EndLine)
	assert.Equal(t, map[string]string{
		"reload":      mod + ".reload", // forward reference, nested in for/match/lambda
		"_aim":        mod + "._aim",
		"call":        "", // reload.call()
		"emit_signal": "",
		"fired":       mod + ".fired", // self.emit_signal only; other.emit_signal must not resolve
		"keys":        "",
	}, targets(shoot, parser.RelCall))
	lines := map[string][]int{}
	for _, r := range shoot.Relations {
		lines[r.TargetName] = append(lines[r.TargetName], r.Line)
	}
	assert.Equal(t, []int{21, 25, 29}, lines["reload"])
	assert.Equal(t, []int{24}, lines["_aim"])
	assert.Equal(t, []int{27}, lines["fired"])

	fired := m[mod+".fired"]
	assert.Equal(t, "Fired.", fired.Doc)
	assert.Equal(t, 33, fired.StartLine)

	hidden := m[mod+"._Hidden"]
	assert.Equal(t, parser.KindClass, hidden.Kind)
	assert.False(t, hidden.Exported)
	assert.Empty(t, hidden.Relations)
	init := m[mod+"._Hidden._init"]
	require.NotEmpty(t, init.Name)
	assert.Equal(t, parser.KindMethod, init.Kind)
	assert.Equal(t, "Make one.", init.Doc)
	assert.False(t, init.Exported)
	assert.Equal(t, map[string]string{"setup": mod + "._Hidden.setup"}, targets(init, parser.RelCall))

	// No extends and no class_name: no class symbol at all, functions are free.
	free := byQN(analyze(t, "/ws/u.gd", "/ws", "func f():\n\tg()\nfunc g(): pass\n"))
	assert.Len(t, free, 2)
	assert.Equal(t, "res://u.g", targets(free["res://u.f"], parser.RelCall)["g"])
	// Private class_name.
	priv := byQN(analyze(t, "/ws/p.gd", "/ws", "class_name _P\nfunc f(): pass\n"))
	assert.False(t, priv["res://p._P"].Exported)
	assert.Equal(t, "_P", priv["res://p._P.f"].Owner)
}

const verifyRound3Scene = `[gd_scene load_steps=4 format=3 uid="uid://c1"]

[ext_resource type="PackedScene" uid="uid://e1" path="res://enemies/slime.tscn" id="1_s"]
[ext_resource type="Script" path="res://weapons/gun.gd" id="2_g"]
[sub_resource type="Animation" id="Animation_1"]
tracks/0/path = NodePath("Gun:rotation")
tracks/0/keys = {
"times": PackedFloat32Array(0, 1)
}

[node name="Arena" type="Node2D"]

[node name="Slime" parent="." instance=ExtResource("1_s")]
position = Vector2(10, 20)

[node name="Gun" parent="Slime" instance=ExtResource("1_s")]
script = ExtResource("2_g")

[node name="Fx" type="GPUParticles2D" parent="Slime/Gun"]
emitting = false

[connection signal="fired" from="Slime/Gun" to="Slime/Gun" method="_aim" flags=3 binds=[1, "x"]]
[connection signal="finished" from="Slime/Gun/Fx" to="Slime" method="_on_fx"]
`

func TestVerifyRound3Scene(t *testing.T) {
	syms := analyze(t, "/ws/levels/arena.tscn", "/ws", verifyRound3Scene)
	m := byQN(syms)
	const mod = "res://levels/arena"
	require.Len(t, syms, 4, "sub_resource and ext_resource are not nodes")
	kinds := map[parser.Kind]int{}
	for _, s := range syms {
		kinds[s.Kind]++
		assert.Equal(t, "gdscript", s.Language)
	}
	assert.Equal(t, map[parser.Kind]int{parser.KindScene: 4}, kinds)

	slime := m[mod+".Arena.Slime"]
	require.NotEmpty(t, slime.Name)
	assert.Equal(t, "res://enemies/slime.tscn", slime.Metadata["instance"])
	assert.Equal(t, ".", slime.Metadata["parent"])
	assert.Equal(t, 13, slime.StartLine)
	assert.Equal(t, 14, slime.EndLine)
	assert.Empty(t, slime.Relations)

	gun := m[mod+".Arena/Slime.Gun"]
	require.NotEmpty(t, gun.Name)
	assert.Equal(t, []parser.Relation{
		{Kind: parser.RelUsesType, TargetName: "gun", TargetQualifiedName: "res://weapons/gun", Line: 17},
		{Kind: parser.RelCall, TargetName: "_aim", TargetQualifiedName: "res://weapons/gun._aim", Line: 22},
	}, gun.Relations)

	fx := m[mod+".Arena/Slime/Gun.Fx"]
	require.NotEmpty(t, fx.Name)
	assert.Equal(t, "GPUParticles2D", fx.Signature)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelCall, TargetName: "_on_fx", Line: 23}}, fx.Relations, "Slime has no script: unresolved")
	assert.Contains(t, fx.Content, "emitting = false")
}

func TestVerifyRound3Big(t *testing.T) {
	var gd strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&gd, "var v%d = %d\n", i, i)
	}
	var tscn strings.Builder
	tscn.WriteString("[gd_scene format=3]\n[node name=\"R\" type=\"Node\"]\nblob = \"" + strings.Repeat("x", 3<<20) + "\"\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&tscn, "[node name=\"N%d\" type=\"Node\" parent=\".\"]\nv = %d\n", i, i)
	}
	for name, tc := range map[string]struct{ path, src string }{
		"gd":   {"/ws/b.gd", gd.String()},
		"tscn": {"/ws/b.tscn", tscn.String()},
	} {
		start := time.Now()
		syms, err := analyzer{}.Analyze(parser.File{Path: tc.path, Root: "/ws", Source: []byte(tc.src)})
		assert.Less(t, time.Since(start), 5*time.Second, name)
		if err == nil {
			assert.NotEmpty(t, syms, name)
		}
	}
}
