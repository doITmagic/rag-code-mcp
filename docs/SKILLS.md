# 🧠 AI Skills System

RagCode MCP uses a **Skills System** to enforce project-specific behaviors, best practices, and workflows. A "Skill" is a structured set of instructions that tells the AI exactly how to operate within your codebase.

---

## 🚀 Managing Skills

### 1. Bundled Skills (Embedded)
These are skills that come built-in with the RagCode binary (e.g., `go-best-practices`, `debugging-guide`).

**To enable a bundled skill:**
Use the `install_skill` tool. This copies the skill files from the binary into your workspace's `.agent/skills/` directory.

```javascript
// Install (activate) a bundled skill
install_skill(skill_id="go-best-practices", active=true)

// Uninstall (deactivate)
install_skill(skill_id="go-best-practices", active=false)
```

### 2. Custom Skills (User Created)
You can create your own skills specific to your project.

**To create (and automatically enable) a custom skill:**
Simply create the skill file directly in the `.agent/skills/` directory.

**Path:** `.agent/skills/<skill-id>/SKILL.md`

**Required YAML Frontmatter:**
You **MUST** include the `compatible-with` field.

```markdown
---
name: <human-readable-name>
description: <short-description-of-functionality>
compatible-with: [rag-code-mcp]
---

# 🦸 Skill: [Name]
... contents ...
```

**Note:** Custom skills created in this folder are **active by default**. You do NOT need to run `install_skill` for them. The `install_skill` tool only works for installing skills *from* the embedded library or external sources. It cannot "install" a skill that is already manually placed in the destination folder.

---

## 📦 Bundled Skills Reference

| Skill ID | Description |
|----------|-------------|
| **`ragcode-priority`** | **MANDATORY**. Enforces using RagCode tools (`search_code`) over grep. |
| **`debugging-guide`** | A 3-step reasoning workflow for root-cause analysis. |
| **`go-best-practices`** | Official Go project layout and patterns. |
| **`python-best-practices`** | Python PEP 8, typing, and testing standards. |
| **`php-laravel`** | Laravel architecture and Eloquent patterns. |
| **`ragcode-update`** | Workflows for updating the MCP server and managing skills. |

---

## 📝 Example `SKILL.md`

```markdown
---
name: secure-coding
description: Enforces security best practices for API endpoints.
compatible-with: [rag-code-mcp]
---

# 🛡️ Skill: Secure Coding

## 🎯 Purpose
To prevent common OWASP vulnerabilities in our API.

## ✅ Mandatory Rules
1. **Input Validation**: All request parameters must be validated using the `validator` library.
2. **Output Encoding**: Never output raw user input to HTML.

## ⛔ Forbidden
- Do NOT use `exec()` or `system()` with user input.
- Do NOT commit secrets or API keys.
```
