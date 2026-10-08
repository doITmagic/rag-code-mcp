# 🐳 Docker Setup for RagCode

RagCode needs two services: **Qdrant** (vector storage) and **Ollama** (embeddings). The installer can run either of them as a Docker container, and does so with plain `docker run` — there is no `docker-compose.yml` to fetch or maintain.

By default Qdrant runs in Docker and Ollama is expected to be installed on the host. Pass `-ollama=docker` to containerise Ollama as well.

## Why run Ollama in Docker?

- **Isolation**: keeps your system clean.
- **Consistency**: pins the version RagCode was tested against.
- **No extra setup**: the installer starts and wires up both containers for you.

## 🚀 Model mapping — no re-downloading

When Ollama runs in Docker, the installer mounts your host model directory into the container:

```bash
-v ~/.ollama:/root/.ollama
```

So:

1. You **don't** re-download models you already have.
2. Models pulled inside the container appear on your host.
3. You save a lot of disk space.

Use `-models-dir /path/to/models` if your models live somewhere other than `~/.ollama`.

### Prerequisites

- Docker installed and running. (Docker Compose is **not** required.)
- **For GPU support:** NVIDIA Container Toolkit installed.
- Existing models in `~/.ollama` — optional, but saves a download.

### Usage

1. **Start the stack** (both services in Docker, with GPU):

   ```bash
   ragcode-installer -ollama=docker -qdrant=docker -gpu
   ```

   Drop `-gpu` to run CPU-only, and `-ollama=docker` if you prefer your host's Ollama.

   On a first install the images have to be downloaded — Ollama's is several GB, so expect a wait. Progress is printed as it downloads.

2. **Verify Ollama is running:**

   ```bash
   docker logs ragcode-ollama
   ```

3. **Check available models (inside the container):**

   ```bash
   docker exec -it ragcode-ollama ollama list
   ```

   *You should see the models from your host here.*

4. **Pull a new model (if needed):**

   ```bash
   docker exec -it ragcode-ollama ollama pull phi3:medium
   ```

### The images

Both are the official upstream images, unmodified:

| Container | Image |
| --- | --- |
| `ragcode-qdrant` | `qdrant/qdrant` |
| `ragcode-ollama` | `ollama/ollama` |

The `ragcode-` prefix is only the container name, chosen so RagCode does not collide with other projects running Qdrant or Ollama on the same machine.

### ⚠️ Troubleshooting

**Installer seems stuck on "Starting container"**
- Older versions downloaded the image silently. Upgrade, or watch progress with `docker pull ollama/ollama` in another terminal.

**"Error: could not connect to ollama"**
- Ensure port `11434` is not already in use by a local Ollama instance.
- Stop the host service before running the container: `systemctl stop ollama` or `pkill ollama`.

**GPU not working**
- Re-run the installer without `-gpu` for CPU-only mode (slower).
- GPU mode needs the NVIDIA Container Toolkit; check with `docker run --rm --gpus all nvidia/cuda:12.0-base nvidia-smi`.
