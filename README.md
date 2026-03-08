<div align="center">
  <img src="./docs/assets/ragcode-banner.png" alt="RagCode MCP - Semantic Code Navigation with AI" width="100%">
</div>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-blue)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-Compatible-green)](https://modelcontextprotocol.io)
![AI Ready](https://img.shields.io/badge/Codebase-AI%20Ready-blueviolet)

</div>

# RagCode MCP: High-Precision Code Search & Context Optimization

> **Maximum Reasoning, Minimum Tokens.** A privacy-first RAG engine that delivers surgical codebase context without wasting AI bandwidth.

**What is RagCode?**  
RagCode enables AI Assistants (like Windsurf, Cursor, Antigravity, or Copilot) to instantly "understand" your entire project without reading thousands of lines of files. It uses **Retrieval-Augmented Generation (RAG)** mixed with deep **Code Graph (AST) analysis** to give the AI context exactly when it needs it.

**Zero Cloud.** Everything runs 100% locally on your machine using Qdrant and Ollama. Your proprietary code never leaves your laptop.

### The Context vs. Reasoning Dilemma

Dumping entire files into an AI's context window destroys its ability to think, whether you use **Cloud models** (Anthropic, OpenAI, Gemini) or **Local models** (Ollama, LM Studio, vLLM).

**RagCode acts as a surgical filter:** Instead of forcing the AI to read 15,000 lines of code, RagCode delivers only the precise 200-token AST chunks it needs.

- **Local Models:** Reclaim limited context windows for pure *reasoning*. Level the playing field and perform enterprise-grade codebase analysis with zero costs and 100% privacy.
- **Cloud Models:** Slash API costs by 95%, reduce input latency, and drastically minimize hallucinations.

---

## 🚀 Quick Install (1 Command)

Get started instantly in 5 minutes with our automated installer.

**Linux (amd64) / WSL:**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./rag-code-install -ollama=docker -qdrant=docker
```

**macOS (Apple Silicon):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_arm64.tar.gz | tar xz && ./rag-code-install -ollama=docker -qdrant=docker
```

**Windows (PowerShell):**
```powershell
Invoke-WebRequest -Uri "https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_windows_amd64.zip" -OutFile "ragcode.zip"
Expand-Archive ragcode.zip -DestinationPath . -Force
.\rag-code-install.exe -ollama=docker -qdrant=docker
```

👉 **[Read the Full QUICKSTART Guide for your first AI Prompt](./QUICKSTART.md)**

---

## 🗺️ Navigate the Documentation

RagCode has evolved into a massively powerful engine. Choose your path:

- 🏃 **[The Vibe Coder Path (Quickstart)](./QUICKSTART.md)** - I just want it to work in my IDE right now.
- 💻 **[The Developer Path (Contributing)](./CONTRIBUTING.md)** - I want to build RagCode locally or contribute code.
- 🤖 **[The AI Agent Path (Headless/HTTP)](./docs/headless-usage.md)** - I am an AI or script trying to query the workspace directly without an IDE.
- 🧠 **[The Architect Path (Docs)](./docs/)** - How RagCode Lite, AST Fallback, and Scoring actually work.

---

## ✨ Cutting-Edge V2 Features

RagCode V2 isn't just a vector database wrapper. It features deep language understanding:

* ⚡ **Zero-Wait Fallback AST Search**: If your codebase is still indexing, RagCode falls back to lexical/AST structural search so you never wait to get work done.
* 🎯 **Path-Scoped Boosts**: The engine automatically detects what file your AI is currently editing and boosts search results from the same folder or related logic.
* 📦 **Nested Workspace Detection**: Monorepos and deeply nested git submodules are handled safely through a unified detection registry.
* 📊 **Telemetry & JSONL Insights**: RagCode analyzes exactly how much context your AI saves and records precise match reasons for why a snippet was chosen.
* 🚀 **Skill Ecosystem (`rag_list_skills`)**: Enhance your codebase on-the-fly. Agents can install custom behavioral skills into `.ragcode/skills` to expand their own capabilities dynamically.

---

## 🛠 Supported Languages

- **Go** - Complete native AST support
- **PHP** - Complete support (including Laravel macros)
- **Python** - Complete native AST support
- **HTML/Markdown** - Structural documentation mappings

---

<div align="center">

**Built with ❤️ for developers who want smarter AI code assistants**

⭐ **[Star us on GitHub](https://github.com/doITmagic/rag-code-mcp)** if RagCode speeds up your workflow!

</div>
