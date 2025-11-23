# 🐳 Docker Setup for RagCode

This guide explains how to run the RagCode infrastructure (Qdrant + Ollama) using Docker, while leveraging your existing local Ollama models.

## Why run Ollama in Docker?

- **Isolation**: Keeps your system clean.
- **Consistency**: Ensures you run the exact version required.
- **Integration**: Easy to orchestrate with Qdrant via `docker-compose`.

## 🚀 The "Smart" Setup (Model Mapping)

We have configured `docker-compose.yml` to map your local Ollama models (`~/.ollama`) into the container. This means:
1. You **don't** need to re-download models.
2. Models downloaded inside Docker appear on your host.
3. You save massive amounts of disk space.

### Prerequisites

- Docker & Docker Compose installed.
- **For GPU Support (Recommended):** NVIDIA Container Toolkit installed.
- Existing models in `~/.ollama` (optional, but recommended).

### Usage

1. **Start the stack:**
   ```bash
   docker-compose up -d
   ```

2. **Verify Ollama is running:**
   ```bash
   docker logs ragcode-ollama
   ```

3. **Check available models (inside container):**
   ```bash
   docker exec -it ragcode-ollama ollama list
   ```
   *You should see all your locally downloaded models here!*

4. **Pull a new model (if needed):**
   ```bash
   docker exec -it ragcode-ollama ollama pull phi3:medium
   ```

### ⚠️ Troubleshooting

**"Error: could not connect to ollama"**
- Ensure port `11434` is not being used by a local Ollama instance.
- Stop your local Ollama before running the container: `systemctl stop ollama` or `pkill ollama`.

**GPU not working**
- If you don't have an NVIDIA GPU or the container toolkit, remove the `deploy` section from `docker-compose.yml` to run in CPU-only mode (slower).
