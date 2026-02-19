# Skills Package

The `skills` package manages project-local extensions and configurations. It allows the MCP server to "install" specific files (like `.cursorrules`, custom scripts, or documentation blueprints) directly into the user's repository.

## Concept

A "Skill" is a set of files that help the user or the AI perform better within a specific codebase.
- **Project Structure**: Blueprints for new modules.
- **Tooling**: Custom terminal scripts or configuration files.
- **Rules**: LLM behavior modifiers (e.g., custom prompts for specific frameworks).

## Embedded Skills

The package contains "embedded" skills that are compiled into the RagCode binary using `go:embed`.
- **`ragcode-sse`**: Helps setting up SSE client connections.
- **Framework Blueprints**: Patterns for Common architectures.

## Usage

```go
import "github.com/doITmagic/rag-code-mcp/internal/skills"

// Install a specific skill to a repository
err := skills.InstallSkill("ragcode-enterprise-patterns", "/path/to/repo")
```

## Security

Skills can only write to the specific repository root detected by the MCP server. They cannot modify system files or files outside the target workspace.
