# Documentation Rebuild Implementation Plan

> Note: Execute this plan task-by-task, completing and validating each step before moving on to the next.

**Goal:** Completely rebuild the RagCode MCP documentation from the source code truth, removing outdated references, translating Romanian to English, and highlighting the ~70% new features.

**Architecture:** A multi-layered documentation approach. `README.md` acts as a clean, vibe-coder-friendly pivot. `QUICKSTART.md` handles immediate onboarding. Advanced technical details live in `docs/`, and internal packages update their respective `README.md` files.

**Tech Stack:** Markdown, Git.

---

### Task 1: Audit and Update Internal READMEs (Translation & Cleanup)

**Files:**
- Modify: `internal/service/tools/README.md`
- Modify: `internal/service/README.md`
- Modify: `internal/updater/README.md`

**Step 1: Translate and Update `internal/service/tools/README.md`**
- Translate headers: "Tool-uri disponibile", "Structura pachetului", etc., to English.
- Update the tool table to show precise V2 tool names (e.g., `rag_search` instead of `rag_search_code`).

**Step 2: Update `internal/service/README.md`**
- Replace references to `rag_search_code` and `rag_hybrid_search` with the current `rag_search` and other V2 tools.

**Step 3: Update `internal/updater/README.md`**
- Refactor the code example to remove fictitious `CheckForUpdates` and `PerformUpdate` method calls if they don't exactly match the current V2 source code.

**Step 4: Commit**
```bash
git add internal/service/tools/README.md internal/service/README.md internal/updater/README.md
git commit -m "docs: translate and update internal READMEs to V2 truth"
```

---

### Task 2: Refactor Root Level `llms.txt` and Move `RagCodeLite.md`

**Files:**
- Modify: `llms.txt`
- Modify: `llms-full.txt`
- Move: `RagCodeLite.md` -> `docs/architecture/sqlite-graph-concept.md`

**Step 1: Update `llms.txt` and `llms-full.txt`**
- Replace the 13 outdated tools with the actual current MCP tools: `rag_search`, `rag_find_usages`, `rag_call_hierarchy`, `rag_list_package_exports`, `rag_read_file_context`, `rag_index_workspace`, `rag_list_skills`, `rag_install_skill`, `rag_evaluate`.
- Remove references to `phi3:medium` or `rag_search_code`.

**Step 2: Translate and Move `RagCodeLite.md`**
- Create `docs/architecture/sqlite-graph-concept.md` and translate the Romanian content of `RagCodeLite.md` to English.
- Delete the original `RagCodeLite.md`.

**Step 3: Commit**
```bash
git rm RagCodeLite.md
git add llms.txt llms-full.txt docs/architecture/sqlite-graph-concept.md
git commit -m "docs: update llm prompts to V2 toolset and translate RagCodeLite"
```

---

### Task 3: Rebuild the Pivot `README.md` and Update `CONTRIBUTING.md`

**Files:**
- Modify: `CONTRIBUTING.md`
- Modify: `README.md`

**Step 1: Update `CONTRIBUTING.md`**
- Replace the Ollama pull instructions for `phi3:medium` and `mxbai-embed-large` with `qwen3-embedding:0.6b`.

**Step 2: Slim Down `README.md`**
- Restructure into 3 specific zones:
  1. Hook/Intro: Explain RAG/MCP simply.
  2. Installation: 1-line script + Quick links to Quickstart/Developer path.
  3. New Features Banner: Highlight AST Fallback, Path-Scoped Search, Telemetry, Skill Ecosystem.
- Move prolonged technical details (like the exhaustive tool list) out to point to `docs/`.

**Step 3: Commit**
```bash
git add README.md CONTRIBUTING.md
git commit -m "docs: slim root README and fix contributing setup instructions"
```

---

### Task 4: Enhance `QUICKSTART.md` and Add Headless Docs

**Files:**
- Modify: `QUICKSTART.md`
- Create: `docs/headless-usage.md`

**Step 1: Refine `QUICKSTART.md`**
- Clean up excessive emojis.
- Ensure the focus is tightly on "5 minutes to first query in your IDE."

**Step 2: Create `docs/headless-usage.md`**
- Document the capability of using RagCode without an IDE via HTTP JSON / SSE (e.g., using the `ragcode-sse` skill) so that direct agent integration is formally documented.

**Step 3: Commit**
```bash
git add QUICKSTART.md docs/headless-usage.md
git commit -m "docs: refine quickstart and add headless HTTP/SSE usage guide"
```
