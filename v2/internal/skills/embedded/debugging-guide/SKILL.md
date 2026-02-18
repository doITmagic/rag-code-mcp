---
name: debugging-guide
description: How to use ragcode tools for fast root-cause analysis and debugging
---

# 🐞 Skill: Debugging with Ragcode

This skill defines a workflow for using ragcode tools to fix bugs efficiently.

---

## 🔍 The 3-Step Debug Workflow

### Step 1: Locate the Symptom
- Search for the exact error message using `hybrid_search`.
- If no error message, search semantically for the failing feature using `search_code`.

### Step 2: Contextualize the Flow
- Find where the failing function is called using `find_implementations`.
- Look at the surrounding logic with `get_code_context` (fetch at least 10 lines before and after).

### Step 3: Analyze Data Models
- Use `find_type_definition` to check if a struct field might be missing or wrongly typed.
- Use `list_package_exports` to see if there are utility functions already solving the problem.

---

## 💡 Troubleshooting Pro-Tip:
If you are lost, search for "logger" or "Error" in the package to see how other parts of the system handle failures.
