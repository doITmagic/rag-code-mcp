# Documentation Rebuild Design (The "Truth-in-Code" Approach)

## 1. Project Context & Goals
The RagCode MCP project has undergone massive recent changes (~50% code modifications, ~70% new features). The existing documentation is severely outdated, referencing old tools, legacy models, and lacking information about major new architectural components (e.g., fallback AST search, telemetry, path-scoped search, nested workspaces, skill systems). 

The goal is to rebuild the documentation from scratch using a "Zero Trust in Old Docs" philosophy: the source code is the only source of truth. The new documentation must be:
- **100% English**: All Romanian text (e.g., in internal READMEs, `RagCodeLite.md`) must be translated.
- **Progressive Disclosure**: Information must be layered. Beginners ("vibe coders") get instant gratification (quickstart), while advanced users get deep-dives in the `docs/` folder.
- **Accurate**: Tool names, parameters, and behaviors must reflect the current V2 implementation.

## 2. Architecture of the Documentation

### Root Directory
- **`README.md`**: The central pivot. Limited to ~150-200 lines. 
  - Hook: What RagCode is (in simple terms).
  - One-line installation.
  - High-level feature list (with emphasis on new features like fallback search, telemetry).
  - Clear navigation links to specialized documents.
- **`QUICKSTART.md`**: The "Vibe Coder" path. Focuses strictly on getting the first query working in an IDE within 5 minutes. Cleaned of excessive emojis.
- **`CONTRIBUTING.md`**: The Developer path. Updated with current models (`qwen3-embedding:0.6b`), testing, and PR flows.
- **`llms.txt` / `llms-full.txt`**: Updated to reflect the current exact V2 toolset (e.g., `rag_search` instead of `rag_search_code`).

### `docs/` Directory Structure
- **`docs/tools/`**: A reference for each specific tool, generated from the actual implementation parameters (e.g., `rag_search`, `rag_find_usages`, `rag_list_skills`).
- **`docs/architecture/`**: 
  - Move and translate `RagCodeLite.md` here (e.g., `docs/architecture/sqlite-graph-concept.md`).
  - Add documentation on Fallback/AST search and Telemetry.
- **`docs/headless-usage.md`**: New documentation explaining HTTP/SSE usage for agents without IDE integration.

### `internal/` Directory Documentation
- All internal READMEs (e.g., `internal/service/tools/README.md`, `internal/updater/README.md`) must be audited, translated to formatting-compliant English, and updated to reflect current internal APIs and structures.

## 3. Key Content Updates Required (From Recent Commits)
The rebuild must explicitly incorporate the following 70% new functionality:
1. **Fallback Direct Search**: Non-blocking search during indexing that falls back to AST-based lexical matching when Qdrant is empty.
2. **Telemetry & Metrics**: Session metrics and JSONL tracking (`.ragcode/search_metrics.jsonl`).
3. **Scoring Enhancements**: Path-scoped boosts/penalties, `min_score` auto-thresholding, and `match_reasons` annotations.
4. **Workspace Detection**: Nested Git repo resolution via `nested_workspace_override`.
5. **Skill Ecosystem**: Documentation of `rag_list_skills` and `rag_install_skill` for managing agent capabilities.
6. **Dynamic Fallback Notes**: Live indexing progress reported back to the AI.

## 4. Execution Rules
- Verify every tool name and parameter against `internal/service/tools/*.go`.
- Verify installation commands against actual release processes.
- Maintain a professional, approachable tone.
- Remove all Romanian comments and text, translating them accurately to English.
