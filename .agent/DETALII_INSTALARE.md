# 🔧 Detalii Tehnice - Procesul de Instalare CodeRAG MCP

**Data:** 2025-11-21  
**Scop:** Documentație tehnică despre ce se întâmplă exact la instalare

---

## 📋 Fluxul Complet de Instalare

### Comanda Utilizatorului
```bash
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | bash
```

---

## 🔄 Pașii Executați Automat

### 1. Verificare Dependențe ✅

**Script:** `quick-install.sh` → funcția `check_prerequisites()`

**Ce verifică:**
- ✅ Docker este instalat (`command -v docker`)
- ✅ Ollama este instalat (`command -v ollama`)

**Dacă lipsesc dependențe:**
```
📦 Instalează dependențele:

  Docker:
    Ubuntu/Debian: sudo apt install docker.io && sudo systemctl start docker
    macOS: brew install docker

  Ollama:
    Linux: curl -fsSL https://ollama.com/install.sh | sh
    macOS: brew install ollama

✗ Instalează dependențele și rulează din nou acest script
```

**Dacă totul e OK:**
```
✓ Toate dependențele sunt instalate
```

---

### 2. Instalare Binare ✅

**Script:** `quick-install.sh` → funcția `install_coderag()`

#### Opțiunea A: Download Release Oficial

**Ce face:**
1. Creează director temporar: `mktemp -d`
2. Descarcă release: `curl -fsSL https://github.com/doITmagic/coderag-mcp/releases/latest/download/coderag-linux.tar.gz`
3. Extrage arhiva: `tar -xzf release.tar.gz`
4. Găsește directorul extras
5. Instalează binare:
   ```bash
   install -m 755 extracted/bin/coderag-mcp ~/.local/share/coderag/bin/coderag-mcp
   install -m 755 extracted/bin/index-all ~/.local/share/coderag/bin/index-all
   ```
6. Copiază scripturi:
   ```bash
   install -m 755 extracted/start.sh ~/.local/share/coderag/start.sh
   cp extracted/config.yaml ~/.local/share/coderag/config.yaml
   ```

**Output:**
```
===> Descarc release-ul oficial...
✓ Binare instalate în ~/.local/share/coderag/bin
```

#### Opțiunea B: Build Local (Fallback)

**Când se folosește:**
- Release-ul nu poate fi descărcat (404, network error, etc.)

**Ce face:**
1. Verifică că Go este instalat
2. Clonează repository-ul:
   ```bash
   git clone https://github.com/doITmagic/coderag-mcp.git
   ```
3. Compilează binarele:
   ```bash
   go build -o ~/.local/share/coderag/bin/coderag-mcp ./cmd/coderag-mcp
   go build -o ~/.local/share/coderag/bin/index-all ./cmd/index-all
   ```
4. Copiază scripturi și config

**Output:**
```
! Nu am putut descărca release-ul, încerc build local...
===> Clonez repository-ul...
===> Compilez binarele...
✓ Build local reușit
```

---

### 3. Configurare PATH ✅

**Script:** `quick-install.sh` → funcția `setup_path()`

**Ce face:**
1. Detectează shell-ul utilizatorului:
   - Bash → `~/.bashrc`
   - Zsh → `~/.zshrc`
2. Verifică dacă PATH-ul e deja configurat
3. Dacă NU, adaugă la sfârșitul fișierului:
   ```bash
   # CodeRAG MCP
   export PATH="~/.local/share/coderag/bin:$PATH"
   ```

**Output:**
```
===> Configurez PATH...
✓ PATH actualizat în ~/.bashrc (reîncarcă shell-ul)
```

**Notă:** Trebuie să reîncarci shell-ul pentru ca PATH-ul să fie activ:
```bash
source ~/.bashrc
# SAU
exec bash
```

---

### 4. Configurare MCP Clients ✅

**Script:** `quick-install.sh` → funcția `configure_mcp()`

**Ce face:**

#### Pentru Windsurf:
1. Creează director: `mkdir -p ~/.codeium/windsurf/`
2. Citește config existent (dacă există): `~/.codeium/windsurf/mcp_config.json`
3. Actualizează config cu Python:
   ```python
   config["mcpServers"]["coderag"] = {
       "command": "~/.local/share/coderag/bin/coderag-mcp",
       "args": [],
       "env": {
           "OLLAMA_BASE_URL": "http://localhost:11434",
           "OLLAMA_MODEL": "phi3:medium",
           "OLLAMA_EMBED": "nomic-embed-text",
           "QDRANT_URL": "http://localhost:6333"
       }
   }
   ```
4. Scrie config actualizat

#### Pentru Cursor:
- Același proces pentru `~/.cursor/mcp.config.json`

**Output:**
```
===> Configurez Windsurf și Cursor...
✓ Config MCP pentru Windsurf: ~/.codeium/windsurf/mcp_config.json
✓ Config MCP pentru Cursor: ~/.cursor/mcp.config.json
```

**Notă:** Trebuie să reîncarci IDE-ul (Windsurf/Cursor) pentru ca configurația să fie activă.

---

### 5. Pornire Servicii ✅

**Script:** `quick-install.sh` → funcția `start_services()` → apelează `start.sh`

#### 5.1. Verificare Docker

**Script:** `start.sh` → funcția `check_docker()`

**Ce face:**
1. Verifică că Docker este instalat: `command -v docker`
2. Verifică că Docker daemon rulează: `docker info`

**Dacă Docker NU rulează:**
```bash
sudo systemctl start docker  # Pe Linux cu systemd
```

**Output:**
```
✓ Docker is available
```

#### 5.2. Pornire Qdrant Container

**Script:** `start.sh` → funcția `start_qdrant()`

**Ce face:**
1. Verifică dacă Qdrant deja rulează: `curl -s http://localhost:6333/readyz`
2. Dacă DA → skip
3. Dacă NU:
   - Creează director pentru date: `mkdir -p ~/.local/share/qdrant`
   - Oprește container vechi (dacă există): `docker stop qdrant && docker rm qdrant`
   - Pornește container nou:
     ```bash
     docker run -d --name qdrant \
       -p 6333:6333 \
       -p 6334:6334 \
       -v ~/.local/share/qdrant:/qdrant/storage \
       qdrant/qdrant
     ```
   - Așteaptă până Qdrant e gata (max 30 secunde)

**Output:**
```
===> Starting Qdrant (global service)...
  Using global data directory: ~/.local/share/qdrant
  Waiting for Qdrant to start...
✓ Qdrant started successfully
  REST API: http://localhost:6333
  gRPC API: localhost:6334
  Data: ~/.local/share/qdrant
```

**Porturi expuse:**
- `6333` - REST API (pentru queries)
- `6334` - gRPC API (pentru operații rapide)

**Date persistente:**
- Toate colecțiile Qdrant sunt salvate în `~/.local/share/qdrant`
- Dacă ștergi containerul, datele rămân
- Dacă ștergi directorul, pierzi toate workspace-urile indexate

#### 5.3. Pornire Ollama

**Script:** `start.sh` → funcția `check_ollama()`

**Ce face:**
1. Verifică că Ollama este instalat: `command -v ollama`
2. Verifică dacă Ollama service rulează: `curl -s http://localhost:11434/api/tags`
3. Dacă NU rulează:
   - Pornește Ollama în background:
     ```bash
     nohup ollama serve > /dev/null 2>&1 &
     ```
   - Așteaptă până service-ul e gata (max 30 secunde)

**Output:**
```
===> Checking Ollama...
✓ Ollama is installed
! Ollama service is not running
  Starting Ollama service in background...
  Waiting for Ollama to start...
✓ Ollama service started
```

**Port expus:**
- `11434` - Ollama API

**Notă:** Ollama rulează ca proces în background, NU ca Docker container.

#### 5.4. Descărcare Modele AI

**Script:** `start.sh` → funcția `check_ollama()` (continuare)

**Ce face:**
1. Verifică dacă `nomic-embed-text` este deja descărcat: `ollama list | grep nomic-embed-text`
2. Dacă NU:
   ```bash
   ollama pull nomic-embed-text
   ```
   - Descarcă ~274 MB
   - Poate dura 1-3 minute (depinde de internet)

3. Verifică dacă `phi3:medium` este deja descărcat: `ollama list | grep phi3:medium`
4. Dacă NU:
   ```bash
   ollama pull phi3:medium
   ```
   - Descarcă ~7.9 GB
   - Poate dura 5-15 minute (depinde de internet)

**Output:**
```
! nomic-embed-text model not found
  Pulling model (this may take a few minutes)...
✓ Model downloaded successfully

! phi3:medium model not found
  Pulling model (this will take several minutes, ~8GB)...
✓ Model downloaded successfully
```

**Modele salvate în:**
- Linux: `~/.ollama/models/`
- macOS: `~/.ollama/models/`

**Notă:** Modelele se descarcă o singură dată. La următoarele rulări, se verifică doar că există.

#### 5.5. Pornire MCP Server

**Script:** `start.sh` (final)

**Ce face:**
1. Pornește MCP server în background:
   ```bash
   ~/.local/share/coderag/bin/coderag-mcp &
   ```

**Output:**
```
===> Starting MCP server...
✓ MCP server started in background
```

**Notă:** MCP server-ul rulează în background și ascultă conexiuni de la Windsurf/Cursor.

---

### 6. Rezumat Final ✅

**Script:** `quick-install.sh` → funcția `show_summary()`

**Output:**
```
🎉 Instalare completă!

────────────────────────────────────────────
📦 Instalare:
   Binar:        ~/.local/share/coderag/bin/coderag-mcp
   Start script: ~/.local/share/coderag/start.sh
   Config:       ~/.local/share/coderag/config.yaml

🔧 Configurare MCP:
   Windsurf:     ~/.codeium/windsurf/mcp_config.json
   Cursor:       ~/.cursor/mcp.config.json

🚀 Următorii pași:
   1. Reîncarcă shell-ul: source ~/.bashrc
   2. Verifică serviciile: docker ps | grep qdrant
   3. Verifică Ollama: ollama list
   4. Deschide Windsurf/Cursor și folosește CodeRAG!

📚 Documentație:
   Quick Start: https://github.com/doITmagic/coderag-mcp/blob/main/QUICKSTART.md
   README:      https://github.com/doITmagic/coderag-mcp/blob/main/README.md
────────────────────────────────────────────
```

---

## 🔍 Verificare Post-Instalare

### Verifică că binarele sunt instalate
```bash
ls -lh ~/.local/share/coderag/bin/
# Ar trebui să vezi:
# coderag-mcp
# index-all
```

### Verifică că PATH-ul e configurat
```bash
echo $PATH | grep coderag
# Ar trebui să vezi: ~/.local/share/coderag/bin
```

### Verifică că Qdrant rulează
```bash
docker ps | grep qdrant
# Ar trebui să vezi containerul qdrant running

curl http://localhost:6333/readyz
# Ar trebui să returneze: OK
```

### Verifică că Ollama rulează
```bash
curl http://localhost:11434/api/tags
# Ar trebui să returneze JSON cu lista de modele

ollama list
# Ar trebui să vezi:
# nomic-embed-text
# phi3:medium
```

### Verifică că MCP server rulează
```bash
ps aux | grep coderag-mcp
# Ar trebui să vezi procesul coderag-mcp
```

### Verifică configurația MCP în Windsurf
```bash
cat ~/.codeium/windsurf/mcp_config.json | jq .mcpServers.coderag
# Ar trebui să vezi configurația CodeRAG
```

---

## 📊 Spațiu Ocupat

### Binare
```
~/.local/share/coderag/bin/coderag-mcp    ~15 MB
~/.local/share/coderag/bin/index-all      ~12 MB
Total binare:                             ~27 MB
```

### Modele AI
```
~/.ollama/models/nomic-embed-text         ~274 MB
~/.ollama/models/phi3:medium              ~7.9 GB
Total modele:                             ~8.2 GB
```

### Date Qdrant (variabil)
```
~/.local/share/qdrant/                    ~100 MB - 10 GB
(depinde de câte workspace-uri indexezi)
```

### Total Aproximativ
```
Minim:  ~8.3 GB  (fără workspace-uri indexate)
Mediu:  ~10 GB   (cu 2-3 workspace-uri mici)
Mare:   ~20 GB   (cu 10+ workspace-uri mari)
```

---

## 🛠️ Troubleshooting

### Eroare: "Docker daemon is not running"

**Cauză:** Docker nu e pornit

**Soluție:**
```bash
# Linux
sudo systemctl start docker
sudo systemctl enable docker  # Pentru autostart

# macOS
open -a Docker  # Pornește Docker Desktop
```

### Eroare: "Ollama service is not running"

**Cauză:** Ollama nu e pornit

**Soluție:**
```bash
ollama serve
# SAU în background:
nohup ollama serve > /dev/null 2>&1 &
```

### Eroare: "Failed to download release"

**Cauză:** Release-ul nu există sau probleme de rețea

**Soluție:** Scriptul cade automat pe build local. Asigură-te că ai Go instalat:
```bash
go version
# Dacă nu e instalat: https://go.dev/doc/install
```

### Modele AI se descarcă foarte lent

**Cauză:** Internet lent sau server Ollama încărcat

**Soluție:**
- Așteaptă - descărcarea poate dura 10-20 minute
- Verifică conexiunea la internet
- Încearcă mai târziu când serverul e mai puțin încărcat

---

## 🔄 Reinstalare / Update

### Pentru a reinstala complet:
```bash
# Șterge instalarea veche
rm -rf ~/.local/share/coderag

# Oprește serviciile
docker stop qdrant && docker rm qdrant
pkill ollama

# Rulează installerul din nou
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | bash
```

### Pentru a păstra datele Qdrant:
```bash
# NU șterge ~/.local/share/qdrant
# Doar reinstalează binarele
rm -rf ~/.local/share/coderag/bin
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | bash
```

---

**Întrebări?** Deschide un Issue pe GitHub: https://github.com/doITmagic/coderag-mcp/issues
