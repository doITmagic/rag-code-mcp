# Analysis: Feature Proposals & Implementations

## 💡 Standout Ideas and Incremental Enhancements

### 1. 🔄 Live Tracking for "Token Savings" & "Cost Avoided"
**Concept:**
- Instead of just calculating saved tokens per request ephemerally, maintain a global tracker (`~/.ragcode/savings.json`) that cumulatively stores `total_tokens_saved` across all sessions.
- Provide a feature that calculates the real-world USD value of the saved tokens based on standard LLM pricing (e.g., Claude 3.5 Sonnet token costs).
- Send this telemetry back to the AI under the MCP `_meta` response so the user directly sees the financial value RagCode generates (e.g., "RagCode saved you $42 this month").

### 2. 🔄 O(1) Fetch via Byte Offsets
**Concept:**
- While extracting AST symbols, store exact `Byte Offsets` (start and end) in addition to Line Numbers.
- When `rag_read_file_context` is called, instead of reading the file line-by-line or using regex, perform a strict `seek()` operation to jump straight to the exact byte. This prevents loading massive files strictly into RAM.

### 3. 🔄 Stable Symbol IDs
**Concept:** 
- Expose fixed, semantic ID targets for every AST Node, such as `{file_path}::{qualified_name}#{kind}` (e.g., `pkg/parser/php/laravel/adapter.go::Parser.Extract#method`).
- Instead of searching, an Agent could request the direct structure of a known unique ID.

### 5. 🔄 Active Symbol Summarization (During Indexing)
**Concept:**
- If a function or class lacks a Docstring, forward the chunk asynchronously to a cheap LLM (like Gemini Flash or Claude Haiku) *during* the indexing phase.
- Pre-generate a "One Line Summary" and embed that summary instead of the raw cryptic code. This drastically improves the semantic vector matching quality for poorly documented code.

---

## 🤖 AI Agent Validated Implementations (Already Deployed)

Based on rigorous real-world Agent usage, the following core features have been definitively implemented to drastically reduce LLM "decision fatigue".

### 6. ✅ IMPLEMENTED — `rag_search`: Dual Search + Adaptive Response
**Status:** Deployed in `internal/service/tools/smart_search.go`

**Challenge:** Agents used to guess between `mode: "exact"` and `"discovery"`. Even when they found results, pulling 5 full files instantly maxed out the context window.
**Solution:**
1. **Parallel Dual Search**: Executes `SearchCode` (Semantic Vector Qdrant) and `HybridSearchCode` (Exact Path/Substrings) simultaneously across Goroutines.
2. **Merging & Deduplication**: Vector IDs are matched, and results are tagged by provenance (`_source: "semantic" | "hybrid" | "both"`).
3. **Adaptive Formatting**:
   - **Compact Mode**: If >4 results are found, returns only the signatures, paths, and scores (costs ~500 tokens).
   - **Full Source**: Returns raw source code *only* for highly-confident, tight matches.

### 7. ✅ IMPLEMENTED — Indexing Status & Health Metrics + Lazy Stale Cleanup
**Status:** Deployed across all `internal/service/tools/` endpoints.

**Challenge:** Agents would search and hallucinate code that had actually been deleted by the user simply because the Qdrant index was stale.
**Solution:**
- **Pre-flight Disk Verification**: `rag_search` verifies `os.Stat` before returning matches.
- **Lazy Stale Cleanup**: Stale results are **filtered out** from the response (they never reach the AI). Additionally, the engine triggers an **async deletion** of all vectors for the stale file from every language collection in the workspace — a self-healing mechanism with a 10-minute dedup cooldown.
- **Auto-Cleanup Warning**: The response includes a `🧹 N stale file(s) detected and filtered out. Auto-cleanup triggered.` warning giving the AI full observability.
- **Chronological Awareness**: The response schema appends `index_age` (e.g., `"3 minutes ago"`) and `indexing_progress` strictly to maintain absolute validity.

### 8. 🔄 PROPOSED — Migrate from `langchaingo` to Native Ollama Client
**Challenge:** Using `langchaingo` masks underlying context cancellations, causing deadlocks.
**Solution:** Replace it fully with the native `github.com/ollama/ollama/api` which provides direct HTTP keep-alive manipulation, native batch embedding capabilities, and proper Context Propagation timeouts.

### 9. ✅ IMPLEMENTED — Smart Search Consolidation
**Challenge:** Agents suffered from "tool overwhelm" when attempting code searches.
**Solution:** Deprecated `rag_search_code` and moved everything explicitly to `rag_search`. Input schemas were simplified to `query` + `include_full_content` boolean overrides.

### 10. ✅ IMPLEMENTED — Markdown Documentation Indexing
**Challenge:** The engine only understood codebase logic, completely blinding the AI to `README.md` architectural guidelines or implementation plans.
**Solution:** Integrated advanced hierarchical chunking (`MarkdownHeaderTextSplitter`) that natively indexes Headings, Tables, and Lists while keeping overlapping sliding windows for vectors. When an AI searches via `include_docs: true`, it searches the markdown chunks simultaneously with source code.

### 11. ✅ IMPLEMENTED — Deep WordPress & WooCommerce Native Parsers
**Challenge:** The baseline PHP Tree-sitter AST parser could not navigate the massive WordPress hook ecosystem.
**Solution:** Created `pkg/parser/php/wordpress/`, a hyper-specialized sub-package that detects explicit CMS structures:
- Native extraction of **Hooks** (`add_action`, `add_filter`, `do_action`).
- Automatic identification of **Custom Post Types**, **Taxonomies**, and **Shortcodes**.
- **WooCommerce Integration**: Specifically isolates `woocommerce_` hooks and shopping cart overrides.
- **Oxygen Builder**: AST scanning for `extends OxyEl`, rendering layouts, and `ct_builder_json` dynamic components.
