---
name: ragcode-update
description: Guide for managing ragcode updates, health checks, and AI skills
---

# 🛠️ Skill: Ragcode Maintenance & Updates

This skill instructs the AI on how to keep the `ragcode-mcp` server up to date and healthy.

---

## 🔄 Update Workflow

### 1. Detection
When you see a notification in the logs like:
`🌟 New version available: X.Y.Z. Run 'rag_apply_update' to upgrade.`

**You MUST:**
1. Inform the user that a new version is available.
2. Briefly explain that the update includes potential bug fixes or new features.
3. Ask for explicit permission: *"Would you like me to apply the update for you now?"*

### 2. Manual Check
If the user asks "is there any update?", use the `rag_check_update` tool with `force: true`.

### 3. Applying Update
When the user says "Yes" or "Apply update":
1. Call `rag_apply_update`.
2. Inform the user that the installation was successful.
3. Remind the user: *"The server needs a restart (or IDE reload) to activate the new version."*

---

## 🧩 Skill Management

When the user wants to enhance your capabilities:
1. Use `rag_list_skills` to see what is bundled in the binary.
2. Explain what each skill does based on its description.
3. Use `rag_install_skill(skill_id, active=true)` to enable it.

---

## 🏥 Health Checks
If the system feels slow or tools fail consistently:
1. Suggest running the health check (CLI flag `--health`).
2. Check if the workspace is indexed. If not, suggest `index_workspace`.

---

## ⚡ Quick Summary of Tools:
- `rag_check_update`: Check GitHub for new releases.
- `rag_apply_update`: Download and apply the latest binary.
- `rag-code-install`: Local installer and environment setup utility.
- `rag_list_skills`: See available AI behaviors.
- `rag_install_skill`: Toggle a specific behavior on/off.
