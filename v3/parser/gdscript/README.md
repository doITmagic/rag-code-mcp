[RagCode v3](../../README.md) › [parser](../README.md) › gdscript

# GDScript and Godot Scene Analyzer

The `gdscript` package indexes Godot projects. It turns `.gd` scripts into
symbols (the script class, functions, signals, variables, constants, enums,
inner classes) and `.tscn` text scenes into one symbol per scene node, with
the scripts they attach and the signal connections they declare.

Scripts use the tree-sitter `gdscript` grammar; scenes are read with a small
line scanner. Both Godot 4 and Godot 3 syntax are handled where the code says
so (annotations vs. `export` / `onready` keywords, the two `connect()` forms,
both `ExtResource` spellings).

## Where it sits

- **Registered** from `init()`; the binary pulls it in through a blank import
  in [`internal/app`](../../internal/app/README.md).
- **Called** by [`index`](../../index/README.md) and
  [`tools`](../../tools/README.md) through `parser.Analyze`.
- **Calls** the tree-sitter helpers, `RelModule` and `Finalize` from
  [parser](../README.md). No file system access.

| Property | Value |
|----------|-------|
| Language | `gdscript` |
| Extensions | `.gd`, `.tscn` |

## Module names

`Module` is the Godot resource path of the file without its extension, so a
script and the scene next to it share a module:

| File (root `/ws`) | Module |
|-------------------|--------|
| `/ws/actors/player.gd` | `res://actors/player` |
| `/ws/main.tscn` | `res://main` |
| a file outside the root, or no root | `res://<stem>` |

Paths found in the source (`extends "res://base/enemy.gd"`,
`ext_resource path=...`) are mapped the same way. A relative path such as
`"enemy.gd"` or `"../base.gd"` is taken from the referring file's directory
and cleaned. Other schemes (`uid://`) cannot be mapped and give an empty
target.

## Scripts (`.gd`)

### Symbols extracted

| Construct | Kind | Owner | Metadata |
|-----------|------|-------|----------|
| `class_name Player` | `class` | | |
| `extends X` when there is no `class_name` | `class` named after the file stem | | |
| `func f()` at script level, `func _init()` | `function` | the script's `class_name`, `""` when none | |
| `func f()` inside an inner class | `method` | inner class name | |
| `class Inner extends Node:` | `class` | | |
| `signal died(reason)` | `variable` | as above | `gd=signal` |
| `var x`, `@export var`, `@onready var` | `variable` | as above | `export=true` / `onready=true` |
| `const MAX = 5` | `constant` | as above | |
| `enum State { IDLE, RUN }` | `constant` | as above | `gd=enum` |
| `enum { A, B }` (unnamed) | one `constant` per value | as above | `gd=enum` |

- **Doc** comes from the `##` (or `#`) comment lines directly above a member;
  annotations between the comment and the member do not break it. A comment
  on the same line as the previous member is ignored.
- `@export` and `@export_*` annotations (except `@export_group`,
  `@export_subgroup`, `@export_category`) set `export=true`, both as
  standalone annotation lines and inline. Godot 3 `export var` / `onready var`
  are recognized too.
- **Exported** is false for names starting with `_`.
- **Signature** is the header up to the body (functions, inner classes) or
  the first line (variables, constants, signals, enums).

### Example

File `/ws/actors/player.gd`:

```gdscript
## A player.
class_name Player extends CharacterBody2D

@export var speed: float = 100.0
signal died(reason)

func move(delta: float) -> void:
	helper()
	died.connect(_on_died)
	died.emit()

func helper():
	pass

func _on_died():
	pass
```

| Qualified name | Kind | Owner |
|----------------|------|-------|
| `res://actors/player.Player` | `class` | |
| `res://actors/player.Player.speed` | `variable` | `Player` |
| `res://actors/player.Player.died` | `variable` | `Player` |
| `res://actors/player.Player.move` | `function` | `Player` |
| `res://actors/player.Player.helper` | `function` | `Player` |
| `res://actors/player.Player._on_died` | `function` | `Player` |

Relations: `Player` extends `CharacterBody2D` (unresolved: it may be an
engine type); `move` calls `helper` -> `res://actors/player.Player.helper`,
`_on_died` -> `res://actors/player.Player._on_died` (connected handler),
`died` -> `res://actors/player.Player.died` (emitted signal), plus the
unresolved `connect` and `emit` calls themselves.

### Relations extracted

| Source construct | Relation | Target qualified name |
|------------------|----------|-----------------------|
| `extends "res://x/y.gd"` / `extends "y.gd"` | `extends` | `res://x/y` (a module) |
| `extends Node` / `extends ClassName` | `extends` | `""` |
| `f()` or `self.f()` (in a function, or a property `set` / `get` body) | `call` | `<module>.<Owner>.f` when the same class body declares `f` (function or signal), else `""`. |
| `super.f()`, `super()` (inside `f`) | `call` to `f` | `<Base>.f` when the class extends a class by name (`extends Base`, also an engine class such as `Node`; a qualified suffix the index resolves when one class has it); `""` for a `res://` path base. |
| `obj.f()`, `$Node.f()`, `get_node(...).f()` | `call` | `""` |
| `emit_signal("died")` | `call` to `died` | Same rule as a bare call. |
| `died.emit()` | `call` to `died` | When `died` is declared in the same class body. |
| Godot 4 `sig.connect(handler)` / `connect("sig", handler)` | `call` to `handler` | Resolved as a member of this class. |
| Godot 3 `connect("sig", self, "handler")` | `call` to `handler` | Resolved only when the target argument is `self`. |

Calls in the inline `set(value):` / `get:` bodies of a property
(`var item_type: int: set(value): ...`) are relations of that variable,
with the same rules as a function body. A named accessor
(`set = _set_b, get = _get_b`) is not a call to that function.

Identical relations (same name, target and line) are reported once. Inner
classes resolve calls only against their own members.

## Scenes (`.tscn`)

The scene file is split at `[...]` header lines. `[ext_resource]` sections
build an id -> path table; each `[node]` becomes a `scene` symbol;
`[connection]` sections become call relations.

| Field | Value |
|-------|-------|
| Name | the node's `name` |
| Owner | the parent path including the root name (`Main`, `Main/Hud`), `""` for the root |
| Signature | the node `type` |
| Metadata | `type`, `parent` (when set), `instance` (path of an instanced scene) |
| Content | the header and property lines of the section, capped at 8 KiB |

| Relation | When |
|----------|------|
| `uses_type` to the attached script, target `res://...` module | the node has `script = ExtResource(...)` |
| `call` to the connection's `method` | on the `from` node; the target is `<script module>.<method>` when the `to` node has an attached script, else `""` |

### Example

```ini
[ext_resource type="Script" path="res://actors/player.gd" id="1_p"]

[node name="Main" type="Node2D"]
script = ExtResource("1_p")

[node name="Body" type="CharacterBody2D" parent="."]

[connection signal="body_entered" from="Body" to="." method="_on_body"]
```

For `/ws/levels/main.tscn`: `res://levels/main.Main` (uses_type ->
`res://actors/player`) and `res://levels/main.Main.Body` (call `_on_body` ->
`res://actors/player._on_body`).

## Known limitations visible in the code

- A signal connected or emitted through another object (`Events.sig.connect`,
  `CombatEvents.done.emit()`) is not a usage of the signal.
- Autoload singletons from `project.godot` are not symbols under their
  autoload name; the script's symbol keeps its own name.
- A function passed as a value (`sort_custom(Battler.sort)`) or named as a
  property accessor (`set = _set_b`) is not a usage.
- A scene emits at most 500 nodes; the last one then carries
  `truncated=true`.
- Node content is capped at 8 KiB, because embedded resources can serialize
  as megabyte-long lines.
- Only the `script` property is read from a node's property lines.
- Class-name base types are never resolved (they may be engine classes).
- `extends "path.gd"` and scene connection targets are built from the script
  **module** (`res://x/y`, `res://x/y.method`), while the symbols of a script
  that declares `class_name` are qualified with that class name
  (`res://x/y.Player.method`). The indexer resolves such an `extends` to the
  module's one top-level class; a connection target to a `class_name` script
  stays unresolved.
- Calls on any receiver other than `self` or a declared signal stay
  unresolved.

## Files

| File | Contents |
|------|----------|
| `gdscript.go` | Analyzer and registration, dispatch between scripts and scenes, `module` (resource path), `resModule` (map a `res://` or relative path to a module), `stem`. |
| `script.go` | The `.gd` analyzer: header pass for `class_name` / `extends`, member collection with doc comments and annotations, inner classes, unnamed enum values, `@export` / `@onready` metadata, `extends` relations, call relations with signal `emit` / `emit_signal` and `connect` handling, calls of inline property `set` / `get` bodies. |
| `scene.go` | The `.tscn` scanner: section splitting, header attribute parsing, `ExtResource` id extraction (Godot 3 and 4), node symbols, attached-script and connection relations, node and content caps. |

### Tests

| File | Covers |
|------|--------|
| `gdscript_test.go` | Script symbols (kinds, owners, lines, signatures, docs, metadata), call relations including signals and connections, `extends` with `res://`, relative and `uid://` paths, Godot 3 keywords, module names, a scene with instances, scripts and connections, the node and content caps, garbage scenes, malformed scripts, registration, concurrency, `super` calls, calls in property `set` / `get` bodies. |
| `zz_verify_test.go` | Verification tests: scripts, scenes, robustness, Godot 4 syntax, CRLF and upper-case scene files, large inputs, and two further rounds of script, scene and big-file probes. |

## Testing

```bash
cd v3
go test ./parser/gdscript/
```

No build tags or external services.

## See also

- [parser](../README.md): symbol model, qualified names, registry
- [index](../../index/README.md): resolves targets workspace-wide
- Other analyzers: [python](../python/README.md), [web](../web/README.md)
