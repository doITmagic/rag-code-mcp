# Contributing to RagCode MCP

First off, thank you for considering contributing to RagCode MCP! It's people like you that make RagCode such a great tool.

## 🛠️ Development Setup

### Prerequisites

- **Go 1.25+**: Required for building the project.
- **Docker**: Required for running the Qdrant vector database.
- **Ollama**: Required for LLM and embedding models.

### Setting up the environment

1. **Fork and clone the repository**
   ```bash
   git clone https://github.com/YOUR_USERNAME/rag-code-mcp.git
   cd rag-code-mcp
   ```

2. **Install dependencies**
   ```bash
   go mod download
   ```

3. **Start required services**
   Ensure Docker and Ollama are running.
   ```bash
   # Start Qdrant
   docker run -d -p 6333:6333 qdrant/qdrant
   
   # Pull embedding models
   ollama pull qwen3-embedding:0.6b
   ```

4. **Run the server locally**
   ```bash
   go run ./cmd/rag-code-mcp
   ```

## 🧪 Testing

We use the standard Go testing framework.

```bash
# Run all tests
go test ./...

# Run tests with race detection
go test -race ./...
```

Please ensure all tests pass before submitting a Pull Request.

## 📝 Coding Standards

- **Formatting**: We use `gofmt`. Please run `go fmt ./...` before committing.
- **Linting**: We recommend using `golangci-lint`.
- **Commits**: We follow [Conventional Commits](https://www.conventionalcommits.org/).
  - `feat: add new tool`
  - `fix: resolve indexing bug`
  - `docs: update README`

## 🚀 Submitting a Pull Request

1. Create a new branch: `git checkout -b feat/my-new-feature`
2. Make your changes and commit them: `git commit -m 'feat: add some feature'`
3. Push to the branch: `git push origin feat/my-new-feature`
4. Submit a pull request!

## 🐛 Reporting Bugs

Bugs are tracked as GitHub issues. When filing an issue, please include:

- Your OS and version
- RagCode version
- Ollama model being used
- Steps to reproduce the issue

## 💡 Feature Requests

We welcome feature requests! Please use the Feature Request issue template and provide as much detail as possible about the use case.

## 📣 Feedback via AI
   
One of the best ways to help us improve is to ask your AI assistant to evaluate its own performance! 
   
If you have RagCode MCP installed, you can ask your AI to run the `rag_evaluate` tool. This tool asks the AI to provide qualitative feedback on its interaction with the codebase, which you can then share with us in an issue or discussion.
   
This helps us understand:
- Which tools are being used most effectively.
- Where the AI gets confused or hits context limits.
- Potential new features that would make AI agents even smarter.

## 📄 License

By contributing, you agree that your contributions will be licensed under its MIT License.
