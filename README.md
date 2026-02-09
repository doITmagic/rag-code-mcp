# RagCode MCP - Semantic Code Navigation for Go Codebases

[![Go Report Card](https://goreportcard.com/badge/github.com/doITmagic/rag-code-mcp)](https://goreportcard.com/report/github.com/doITmagic/rag-code-mcp)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

RagCode is a **Model Context Protocol (MCP) server** that instantly makes your project **AI-ready**. It enables AI assistants like **GitHub Copilot**, **Cursor**, **Windsurf**, and **Claude** to understand your entire codebase through **semantic vector search**, bridging the gap between your code and Large Language Models (LLMs).

Built with the official [Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk), RagCode provides **10 powerful tools** to index, search, and analyze code, making it the ultimate solution for **AI-ready software development**.

<!--
## ⚖️ The Golden Rule
> **"FOR ANY INFORMATION ABOUT YOUR CODE (location, structure, logic, or usage), YOU MUST USE RAGCODE MCP TOOLS."**
> 
> *By using semantic search instead of simple keyword lookups, your AI assistant gains true context, avoiding hallucinations and missing details even in massive legacy mono-repos.*
-->

---

## ⚡ One-Command Installation

**No Go, no build tools, no configuration needed. Just Docker.**

```bash
curl -sSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/install.sh | bash
```

This script will:
1.  Check for **Docker** and **Ollama**.
2.  Install the `ragcode-installer` binary to `~/.local/bin`.
3.  Automatically provision the vector database (**Qdrant**) and AI models.
4.  Configure your IDEs (**VS Code**, **Cursor**, **Windsurf**, **Claude Desktop**) to use the RagCode MCP server.

---

## 🚀 Key Features

-   **Zero Configuration**: Works out of the box with Docker. Automatic IDE setup for Cursor, VS Code, and Claude.
-   **Semantic Search**: Finds code by meaning, not just exact keywords.
-   **Deep Analysis**: High-precision extraction of functions, methods, and types.
-   **Cross-IDE Support**: One server for all your favorite AI-powered editors.
-   **100% Local & Private**: Your code never leaves your machine. Embeddings and vector storage are entirely local.
-   **Skill System**: Dynamically install specialized AI instructions (e.g., Go Best Practices) into your agent's context.

---

## 🛠 Available Tools

| Tool | Description |
| :--- | :--- |
| `rag_search_code` | Semantic search for functions, classes, and methods. |
| `rag_hybrid_search` | Combined keyword + semantic search for exact identifiers. |
| `rag_get_function_details` | Get complete source code and signature for a specific function. |
| `rag_find_implementations` | Find all callers and implementations of a symbol. |
| `rag_find_type_definition` | Get full definition of a class, struct, or interface. |
| `rag_list_package_exports` | List all public symbols exported by a package/module. |
| `rag_get_code_context` | Read specific lines from a file with surrounding context. |
| `rag_index_workspace` | Manually (re)index a project for semantic search. |
| `list_skills` | List available AI skills bundled in the binary. |
| `install_skill` | Activate or deactivate a skill in the current workspace. |
| `check_update` | Check for newer versions of RagCode. |
| `apply_update` | Automatically download and apply the latest update. |

---

## 📖 Documentation

-   [Quick Start Guide](QUICKSTART.md)
-   [Architecture Overview](docs/architecture.md)
-   [IDE Setup Detailes](docs/IDE-SETUP.md)
-   [Configuration Reference](docs/CONFIGURATION.md)
-   [Troubleshooting](docs/TROUBLESHOOTING.md)

---

## 🤝 Community & Support

-   **GitHub Issues**: [Report bugs or request features](https://github.com/doITmagic/rag-code-mcp/issues)
-   **Contributing**: [Read our contributing guidelines](CONTRIBUTING.md)

---

Developed with ❤️ by **[doITmagic](https://doitmagic.com)**.
