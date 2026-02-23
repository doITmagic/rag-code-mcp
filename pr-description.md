# feat: refine installer, updater security and embedding-only LLM provider

## Description

Refines the installer, updater, and LLM provider to reduce complexity and improve security and reliability across all platforms.

**Changes included:**

- **Unified installer flags** — single entrypoint with consistent CLI flags for local and Docker-based Ollama/Qdrant setup
- **Smart Ollama/Qdrant detection** — interactive fallback between local process and Docker container at startup
- **Healthcheck restricted to embed model only** — removed chat model dependency from health checks, aligning with embedding-only provider
- **Archive extraction replaced with `codeclysm/extract/v3`** — removed ~160 lines of manual `untar`/`unzip` code; ZipSlip, symlink safety, and decompression bombs handled by the library on all OS
- **`OllamaLLMProvider` simplified to embedding-only** — removed `chatModel`, `convertOptions`, dual-model fallback logic; `Generate`/`GenerateStream` return explicit error; single Ollama client per instance

Please include a summary of the change and which issue is fixed. Please also include relevant motivation and context.

Fixes # (issue)

## Type of change

- [ ] Bug fix (non-breaking change which fixes an issue)
- [x] New feature (non-breaking change which adds functionality)
- [x] Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] Documentation update

> **Breaking**: `OllamaLLMProvider.Generate` / `GenerateStream` now return an error. Any caller relying on text generation must switch to a dedicated chat provider.

## Checklist:

- [x] I have performed a self-review of my own code
- [ ] I have formatted my code with `go fmt ./...`
- [x] I have run tests `go test ./...` and they pass
- [x] I have verified integration with Ollama/Qdrant (if applicable)
- [ ] I have updated the documentation accordingly
