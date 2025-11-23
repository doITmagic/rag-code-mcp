#!/bin/bash
set -euo pipefail

REPO_SLUG="doITmagic/rag-code-mcp"
DEFAULT_RELEASE_URL="https://github.com/${REPO_SLUG}/releases/latest/download/ragcode-linux.tar.gz"
RELEASE_URL="${RAGCODE_RELEASE_URL:-$DEFAULT_RELEASE_URL}"
INSTALL_DIR="${HOME}/.local/share/ragcode"
BIN_DIR="${INSTALL_DIR}/bin"
TMP_DIR="$(mktemp -d)"
COLOR_BLUE='\033[0;34m'
COLOR_GREEN='\033[0;32m'
COLOR_YELLOW='\033[1;33m'
COLOR_RED='\033[0;31m'
COLOR_RESET='\033[0m'

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

log() {
    printf "${COLOR_BLUE}==> %s${COLOR_RESET}\n" "$1"
}

success() {
    printf "${COLOR_GREEN}✓ %s${COLOR_RESET}\n" "$1"
}

warn() {
    printf "${COLOR_YELLOW}! %s${COLOR_RESET}\n" "$1"
}

fail() {
    printf "${COLOR_RED}✗ %s${COLOR_RESET}\n" "$1"
    exit 1
}

require_commands() {
    for cmd in "$@"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            fail "Command '$cmd' is required for the installer."
        fi
    done
}

fetch_release_archive() {
    local archive_path="$TMP_DIR/release.tar.gz"
    log "Downloading RagCode release (may take a while)..."
    curl -fsSL "$RELEASE_URL" -o "$archive_path" || fail "Could not download release from $RELEASE_URL"
    echo "$archive_path"
}

extract_release() {
    local archive="$1"
    log "Extracting archive to temporary directory"
    tar -xzf "$archive" -C "$TMP_DIR" || fail "Extraction failed"
    local extracted_root
    extracted_root="$(find "$TMP_DIR" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
    if [ -z "$extracted_root" ]; then
        fail "No valid content found in downloaded release"
    fi
    echo "$extracted_root"
}

install_payload() {
    local src="$1"
    mkdir -p "$BIN_DIR"
    log "Copying binaries to ${BIN_DIR}"
    install -m 755 "$src/bin/rag-code-mcp" "$BIN_DIR/rag-code-mcp" || fail "Missing rag-code-mcp binary in release"
    install -m 755 "$src/bin/index-all" "$BIN_DIR/index-all" || fail "Missing index-all binary in release"

    mkdir -p "$INSTALL_DIR"
    log "Copying scripts to ${INSTALL_DIR}"
    install -m 755 "$src/start.sh" "$INSTALL_DIR/start.sh"

    if [ -f "$src/config.yaml" ] && [ ! -f "$INSTALL_DIR/config.yaml" ]; then
        cp "$src/config.yaml" "$INSTALL_DIR/config.yaml"
        success "Default config copied to ${INSTALL_DIR}/config.yaml"
    elif [ ! -f "$INSTALL_DIR/config.yaml" ]; then
        warn "Default config missing from release; create ${INSTALL_DIR}/config.yaml manually if needed"
    fi
}

ensure_path_export() {
    local profile
    local entry="export PATH=\"${BIN_DIR}:\$PATH\""
    for profile in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
        if [ -f "$profile" ] && grep -F "$BIN_DIR" "$profile" >/dev/null 2>&1; then
            return
        fi
    done

    profile="$HOME/.bashrc"
    log "Adding ${BIN_DIR} to PATH in ${profile}"
    echo "export PATH=\"${BIN_DIR}:\$PATH\"" >> "$profile"
    success "PATH updated in ${profile} (reload shell to apply)"
}

configure_mcp_client() {
    local target="$1"
    local label="$2"
    local path
    path="$(eval echo "$target")"

    mkdir -p "$(dirname "$path")"

    python3 <<PY
import json, os, pathlib
path = pathlib.Path("$path")
config = {}
if path.exists():
    try:
        config = json.loads(path.read_text())
    except Exception:
        pass
config["ragcode"] = {
    "command": "$BIN_DIR/rag-code-mcp",
    "args": [],
    "env": {
        "OLLAMA_BASE_URL": "http://localhost:11434",
        "OLLAMA_MODEL": "phi3:medium",
        "OLLAMA_EMBED": "nomic-embed-text",
        "QDRANT_URL": "http://localhost:6333"
    }
}
path.write_text(json.dumps(config, indent=2) + "\n")
PY
    success "MCP Config updated for ${label}: ${path}"
}

run_setup_once() {
    log "Running start.sh to check services (without starting MCP)..."
    RAGCODE_SKIP_SERVER_START=1 "$INSTALL_DIR/start.sh"
}

main() {
    require_commands curl tar python3

    local archive src
    archive="$(fetch_release_archive)"
    src="$(extract_release "$archive")"
    install_payload "$src"
    ensure_path_export

    configure_mcp_client "${HOME}/.codeium/windsurf/mcp_config.json" "Windsurf"
    configure_mcp_client "${HOME}/.cursor/mcp.config.json" "Cursor"

    run_setup_once

    echo ""
    success "Installation complete!"
    cat <<INFO
${COLOR_BLUE}────────────────────────────────────────────${COLOR_RESET}
Binary:       ${BIN_DIR}/rag-code-mcp
Start script: ${INSTALL_DIR}/start.sh
MCP Config:   ~/.codeium/windsurf/mcp_config.json, ~/.cursor/mcp.config.json

To start server manually:
  ${INSTALL_DIR}/start.sh
INFO
}

main "$@"
