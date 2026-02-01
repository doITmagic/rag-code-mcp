# RagCode MCP Roadmap & Future Ideas

This document outlines potential future enhancements and new tools for the RagCode MCP server, aiming to evolve it from a semantic search engine into a comprehensive code intelligence assistant.

## 🚀 Advanced Analysis Tools (The "Evaluate" Concept)

Moving beyond "finding" code to "assessing" and "explaining" it.

### 1. Architecture & Dependency Mapping (`explain_architecture`)
**Concept:** A tool that explores the dependency graph (already partially indexed) to explain how components interact.
- **Input:** A symbol (e.g., `FactCheckClient`) or a package path.
- **Output:** A structured explanation of incoming/outgoing dependencies and data flow.
- **Use Case:** "If I modify this `FactCheck` tool, what other parts of the system might break?" or "How does data flow from the API controller to the database?"

### 2. Code Quality & Refactoring Advisor (`assess_quality`)
**Concept:** Automated, on-demand code review powered by RAG context + LLM reasoning.
- **Input:** File path or function name.
- **Analysis:**
    - Cyclomatic complexity.
    - Readability score.
    - Adherence to language idioms (Go/PHP/Python specific).
    - Missing documentation/tests.
- **Output:** A JSON report with scores and actionable refactoring suggestions.
- **Use Case:** "Evaluate the quality of the `pkg/retrieval` package before I submit my PR."

### 3. Semantic Context & History (`explain_context`)
**Concept:** Answering "Why?" rather than just "What?". Combines code implementation with documentation and git history (future integration).
- **Input:** A block of code or a specific design decision.
- **Analysis:** Correlates code with related `SEARCH_DOCS` results and potentially commit message history.
- **Output:** A narrative explanation of the design intent.
- **Use Case:** Onboarding new developers. "Why do we use a custom HTTP client here instead of the default one?"

## 🛠️ Infrastructure Enhancements

- **Git History Indexing**: Index commit messages and diffs to answer "When did this break?" queries.
- **Tree-Sitter Support for All Languages**: Standardize parsing for JS/TS/Rust (currently planned).
- **Live Diagnostics**: Integration with LSP (Language Server Protocol) diagnostics to filter search results by "code with errors".

### 4. AI Introspection & ROI Metrics (`get_session_impact`)
**Concept:** A meta-tool that allows the AI to evaluate the utility of RagCode in the current session based on hard data.
- **Input:** None (evaluates current session logs).
- **Analysis:**
    - Calculates "Time Saved" vs traditional naive file reading.
    - Measures "Semantic Precision" (how high were the similarity scores?).
    - Estimates "Token Economy" (how much context passed to LLM vs scanning entire repo).
- **Output:** A JSON report of the session's efficiency.
- **Use Case:** User asks: "How useful was RagCode just now?" -> AI replies: "Extremely useful. I avoided reading 15 files and navigated directly to the relevant function in 0.3s, saving ~5000 tokens."
