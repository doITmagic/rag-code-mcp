# Contributing to RagCode MCP

First off, thank you for considering contributing to RagCode MCP! It's people like you that make RagCode such a great tool.

## 🛠️ Development Setup

### Prerequisites

- **Go 1.26+** (`go.mod`).
- Nothing else: no Docker, no Ollama. To run the server, the embedding runtime
  (llama.cpp `llama-server`) and the model are downloaded into `~/.ragcode` on
  first use (about 310 MB for the model); the unit tests use a fake embedder or
  a stand-in server. No database server is needed: the index is an embedded
  SQLite file.

### Setting up the environment

1. **Fork and clone the repository**
   ```bash
   git clone https://github.com/YOUR_USERNAME/rag-code-mcp.git
   cd rag-code-mcp
   ```

2. **Run the server locally**
   ```bash
   go run ./cmd/rag-code-mcp
   ```
   The first search or index run downloads the runtime and model and starts
   `llama-server` as a child process. Set `RAGCODE_HOME` to a temporary
   directory to keep it away from your real `~/.ragcode`.

Every package directory has a README: start from [README.md](README.md),
"Code map".

## 🧪 Testing

We use the standard Go testing framework.

```bash
go build ./... && go vet ./... && go test -race -count=1 ./...
```

Please ensure all tests pass before submitting a Pull Request.

## 📝 Coding Standards

- **Formatting**: We use `gofmt`. Please run `go fmt ./...` before committing.
- **Linting**: We recommend using `golangci-lint`.
- **Docs**: a change in a package updates its README and, when it changes
  what users see, the root README.
- **Commits**: We follow [Conventional Commits](https://www.conventionalcommits.org/).
  - `feat: add new tool`
  - `fix: resolve indexing bug`
  - `docs: update README`

## 🚀 Submitting a Pull Request

1. Create a new branch from `main`: `git checkout -b feat/my-new-feature`
2. Make your changes and commit them: `git commit -m 'feat: add some feature'`
3. Push to the branch: `git push origin feat/my-new-feature`
4. Open a pull request against `main`.

## 🐛 Reporting Bugs

Bugs are tracked as GitHub issues. When filing an issue, please include:

- Your OS and version
- RagCode version
- Embedding runtime and model (`rag_server_info` shows them as `embedder`)
- Steps to reproduce the issue

## 💡 Feature Requests

We welcome feature requests! Please use the Feature Request issue template and provide as much detail as possible about the use case.

## 📣 Feedback via AI
   
One of the best ways to help us improve is to ask your AI assistant to evaluate its own performance! 
   
If you have RagCode MCP installed, ask your AI assistant how well the `rag_*` tools answered its questions, and share its feedback with us in an issue or discussion.
   
This helps us understand:
- Which tools are being used most effectively.
- Where the AI gets confused or hits context limits.
- Potential new features that would make AI agents even smarter.

## 📄 License

By contributing, you agree that your contributions will be licensed under its MIT License.
