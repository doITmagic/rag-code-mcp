# Skills Package

The `skills` package manages AI agent skill packs for the workspace. It connects
to the [doITmagic/ai-agent-skills](https://github.com/doITmagic/ai-agent-skills)
registry on GitHub, downloads skill folders on demand, and installs them into the
standard locations recognized by popular AI coding tools.

---

## How it works

1. **`ListAvailableSkills()`** — fetches `registry.json` from the GitHub registry and returns the list of available skills with their metadata.
2. **`InstallSkill(skillID, workspaceRoot, target)`** — looks up the skill's path in the registry, downloads the repo tarball from GitHub, and extracts only the requested skill folder into the workspace.
3. **`UninstallSkill(skillID, workspaceRoot)`** — removes the skill from all known locations.
4. **`IsSkillInstalled(skillID, workspaceRoot)`** — checks all known tool directories for the skill.

No skills are compiled into the binary. Everything is fetched live from GitHub.

---

## Supported install targets

Skills are installed into the directory convention of the target AI tool:

| Target key | Directory | Used by |
|------------|-----------|---------|
| `agent` (default) | `.agent/skills/` | Antigravity, rag-code-mcp, OpenCode |
| `agents` | `.agents/skills/` | GitHub Copilot, VS Code |
| `claude` | `.claude/skills/` | Claude (Anthropic) |
| `cursor` | `.cursor/skills/` | Cursor |
| `windsurf` | `.windsurf/skills/` | Windsurf (Codeium) |

Detection (`IsSkillInstalled`, `FindSkillPath`, `UninstallSkill`) checks **all 5 locations** automatically.

---

## Usage

```go
import "github.com/doITmagic/rag-code-mcp/internal/skills"

// List available skills from the remote registry
available, err := skills.ListAvailableSkills()

// Install into .agent/skills/ (default)
err = skills.InstallSkill("oxygen-builder", "/path/to/repo", "agent")

// Install into .cursor/skills/ for Cursor users
err = skills.InstallSkill("oxygen-builder", "/path/to/repo", "cursor")

// Check installation (scans all 5 tool directories)
installed := skills.IsSkillInstalled("oxygen-builder", "/path/to/repo")

// Find exact path where skill is installed
path := skills.FindSkillPath("oxygen-builder", "/path/to/repo")

// Remove from all locations
err = skills.UninstallSkill("oxygen-builder", "/path/to/repo")
```

---

## Testing

Unit tests (no network):
```bash
go test ./internal/skills/
```

Integration tests (real HTTP to GitHub):
```bash
go test -tags integration -v -timeout 120s ./internal/skills/ -run "TestIntegration"
```

---

## Security

- Skill IDs are validated against `^[a-z0-9-]+$` to prevent path traversal.
- Extracted tar entries are checked against the destination directory prefix.
- Skills can only write inside the workspace root detected by the MCP server.
