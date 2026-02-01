#!/bin/bash
#
# CodeRAG MCP - One-Line Installer
# 
# Usage:
#   Production (main branch):
#     curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | bash
#   
#   Development (develop branch):
#     curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/develop/quick-install.sh | BRANCH=develop bash
#
#   Or specify branch explicitly when running locally:
#     BRANCH=develop bash quick-install.sh
#
#   Note: When using curl | bash, environment variables MUST come AFTER the pipe (|)
#

set -euo pipefail

# Colors
BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
RESET='\033[0m'

# Configuration
REPO_SLUG="doITmagic/coderag-mcp"
BRANCH="${BRANCH:-main}"  # Default to 'main' branch, can be overridden with BRANCH env var
INSTALL_DIR="${HOME}/.local/share/coderag"
BIN_DIR="${INSTALL_DIR}/bin"

log() { printf "${BLUE}==>${RESET} %s\n" "$1"; }
success() { printf "${GREEN}✓${RESET} %s\n" "$1"; }
warn() { printf "${YELLOW}!${RESET} %s\n" "$1"; }
fail() { printf "${RED}✗${RESET} %s\n" "$1"; exit 1; }

# Check if command exists
has_command() {
    command -v "$1" >/dev/null 2>&1
}

# Step 1: Check prerequisites
check_prerequisites() {
    log "Checking system requirements..."
    
    local missing=()
    
    # Check Docker
    if ! has_command docker; then
        missing+=("docker")
    fi
    
    # Check Ollama
    if ! has_command ollama; then
        missing+=("ollama")
    fi
    
    if [ ${#missing[@]} -gt 0 ]; then
        warn "Missing dependencies: ${missing[*]}"
        echo ""
        echo "📦 Install dependencies:"
        echo ""
        
        for dep in "${missing[@]}"; do
            case "$dep" in
                docker)
                    echo "  Docker:"
                    echo "    Ubuntu/Debian: sudo apt install docker.io && sudo systemctl start docker"
                    echo "    macOS: brew install docker"
                    ;;
                ollama)
                    echo "  Ollama:"
                    echo "    Linux: curl -fsSL https://ollama.com/install.sh | sh"
                    echo "    macOS: brew install ollama"
                    ;;
            esac
            echo ""
        done
        
        fail "Please install dependencies and run this script again"
    fi
    
    success "All dependencies are installed"
}

# Step 2: Download and install
install_coderag() {
    log "Installing CodeRAG MCP..."
    
    # Create temp directory
    local tmp_dir=$(mktemp -d)
    trap "rm -rf $tmp_dir" EXIT
    
    # Try to download release
    local release_url="https://github.com/${REPO_SLUG}/releases/latest/download/coderag-linux.tar.gz"
    
    if curl -fsSL "$release_url" -o "$tmp_dir/release.tar.gz" 2>/dev/null; then
        log "Downloading official release..."
        tar -xzf "$tmp_dir/release.tar.gz" -C "$tmp_dir"
        
        # Find extracted directory
        local extracted=$(find "$tmp_dir" -mindepth 1 -maxdepth 1 -type d | head -n 1)
        
        if [ -z "$extracted" ]; then
            fail "Could not extract release"
        fi
        
        # Install binaries
        mkdir -p "$BIN_DIR"
        install -m 755 "$extracted/bin/coderag-mcp" "$BIN_DIR/coderag-mcp"
        install -m 755 "$extracted/bin/index-all" "$BIN_DIR/index-all"
        
        # Install scripts and config
        install -m 755 "$extracted/start.sh" "$INSTALL_DIR/start.sh"
        
        if [ -f "$extracted/config.yaml" ] && [ ! -f "$INSTALL_DIR/config.yaml" ]; then
            cp "$extracted/config.yaml" "$INSTALL_DIR/config.yaml"
        fi
        
        success "Binaries installed in $BIN_DIR"
    else
        warn "Could not download release, falling back to prebuilt binaries from repository..."

        mkdir -p "$BIN_DIR"
        mkdir -p "$INSTALL_DIR"

        base_bin_url="https://raw.githubusercontent.com/${REPO_SLUG}/${BRANCH}/bin"
        base_repo_url="https://raw.githubusercontent.com/${REPO_SLUG}/${BRANCH}"

        # Download prebuilt binaries
        for f in coderag-mcp index-all; do
            # Remove existing binary if present
            if [ -f "$BIN_DIR/$f" ]; then
                log "Removing existing $f for upgrade..."
                rm -f "$BIN_DIR/$f"
            fi
            
            if curl -fsSL "$base_bin_url/$f" -o "$BIN_DIR/$f"; then
                chmod +x "$BIN_DIR/$f"
            else
                fail "Failed to download $f from $base_bin_url"
            fi
        done

        # Download start.sh
        if curl -fsSL "$base_repo_url/start.sh" -o "$INSTALL_DIR/start.sh"; then
            chmod +x "$INSTALL_DIR/start.sh"
        else
            warn "Could not download start.sh from repository"
        fi

        # Download default config.yaml only if not present
        if [ ! -f "$INSTALL_DIR/config.yaml" ]; then
            if curl -fsSL "$base_repo_url/config.yaml" -o "$INSTALL_DIR/config.yaml"; then
                :
            else
                warn "Could not download config.yaml from repository"
            fi
        fi

        success "Prebuilt binaries installed from repository"
    fi
}

# Step 3: Add to PATH
setup_path() {
    log "Configuring PATH..."
    
    local shell_rc="${HOME}/.bashrc"
    
    # Detect shell
    if [ -n "${ZSH_VERSION:-}" ]; then
        shell_rc="${HOME}/.zshrc"
    fi
    
    # Check if already in PATH
    if grep -q "$BIN_DIR" "$shell_rc" 2>/dev/null; then
        success "PATH already configured"
        return
    fi
    
    # Add to PATH
    echo "" >> "$shell_rc"
    echo "# CodeRAG MCP" >> "$shell_rc"
    echo "export PATH=\"$BIN_DIR:\$PATH\"" >> "$shell_rc"
    
    success "PATH updated in $shell_rc (reload shell to apply)"
}

# Step 4: Configure MCP clients
configure_mcp() {
    log "Configuring IDEs (Windsurf, Cursor, Antigravity, VS Code)..."
    
    local configs=(
        "${HOME}/.codeium/windsurf/mcp_config.json:Windsurf"
        "${HOME}/.cursor/mcp.config.json:Cursor"
        "${HOME}/.gemini/antigravity/mcp_config.json:Antigravity"
        "${HOME}/.config/Code/User/globalStorage/mcp-servers.json:VS Code"
    )
    
    for entry in "${configs[@]}"; do
        IFS=: read -r config_path label <<< "$entry"
        
        mkdir -p "$(dirname "$config_path")"
        
        # Read existing config or create new
        local config="{}"
        if [ -f "$config_path" ]; then
            config=$(cat "$config_path")
        fi
        
        # Update config with Python
        python3 <<PY
import json, pathlib, sys

try:
    config_path = pathlib.Path("$config_path")
    print(f"Processing {config_path}...")
    
    config = {}
    
    # Try to read existing config
    if config_path.exists():
        try:
            content = config_path.read_text()
            if content.strip():
                config = json.loads(content)
        except Exception as e:
            print(f"Warning: Could not parse existing config (might be empty or invalid JSON): {e}")
            print("Creating new config with preserved content if possible...")
            # If we can't parse it, we might overwrite it. 
            # For now, let's assume we start fresh but keep backup
            if config_path.exists():
                backup_path = config_path.with_suffix('.json.bak')
                config_path.rename(backup_path)
                print(f"Backed up invalid config to {backup_path}")

    # Ensure mcpServers exists
    if "mcpServers" not in config:
        config["mcpServers"] = {}

    # Add coderag to mcpServers
    config["mcpServers"]["coderag"] = {
        "command": "$BIN_DIR/coderag-mcp",
        "args": [],
        "env": {
            "OLLAMA_BASE_URL": "http://localhost:11434",
            "OLLAMA_MODEL": "phi3:medium",
            "OLLAMA_EMBED": "nomic-embed-text",
            "QDRANT_URL": "http://localhost:6333"
        }
    }

    # Remove top-level coderag if exists (old format)
    if "coderag" in config and "mcpServers" in config:
        del config["coderag"]

    # Write config
    config_path.parent.mkdir(parents=True, exist_ok=True)
    config_path.write_text(json.dumps(config, indent=2) + "\n")
    print(f"Successfully wrote config to {config_path}")

except Exception as e:
    print(f"Error updating config: {e}")
    sys.exit(1)
PY
        
        success "MCP Config for $label: $config_path"
    done
}

# Step 5: Start services
start_services() {
    log "Starting required services..."
    
    # Check if Docker is running
    if ! docker ps > /dev/null 2>&1; then
        warn "Docker is not running. Attempting to start..."
        
        if has_command systemctl; then
            sudo systemctl start docker || warn "Could not start Docker automatically"
        fi
    fi
    
    # Run start.sh to setup Qdrant and Ollama (but skip MCP server start)
    log "Setting up Qdrant and Ollama..."
    
    if [ -f "$INSTALL_DIR/start.sh" ]; then
        # Run start.sh with CODERAG_SKIP_SERVER_START=1 to only setup services
        if CODERAG_SKIP_SERVER_START=1 bash "$INSTALL_DIR/start.sh"; then
            success "Services started successfully"
        else
            warn "Some services may not have started. Check with: coderag-mcp -health"
        fi
    else
        warn "start.sh not found at $INSTALL_DIR/start.sh"
        warn "You may need to start Qdrant and Ollama manually"
    fi
}

# Step 6: Show summary
show_summary() {
    echo ""
    success "🎉 Installation complete!"
    echo ""
    echo -e "${BLUE}────────────────────────────────────────────${RESET}"
    echo "📦 Installation:"
    echo "   Binary:       $BIN_DIR/coderag-mcp"
    echo "   Start script: $INSTALL_DIR/start.sh"
    echo "   Config:       $INSTALL_DIR/config.yaml"
    echo ""
    echo "🔧 MCP Configuration:"
    echo "   Windsurf:     ~/.codeium/windsurf/mcp_config.json"
    echo "   Cursor:       ~/.cursor/mcp.config.json"
    echo "   Antigravity:  ~/.gemini/antigravity/mcp_config.json"
    echo "   VS Code:      ~/.config/Code/User/globalStorage/mcp-servers.json"
    echo ""
    echo "🚀 Next Steps:"
    echo "   1. Reload shell: source ~/.bashrc"
    echo "   2. Verify services: docker ps | grep qdrant"
    echo "   3. Verify Ollama: ollama list"
    echo "   4. Open Windsurf/Cursor and use CodeRAG!"
    echo ""
    echo -e "${GREEN}💡 First Time Setup - Index Your Workspace:${RESET}"
    echo ""
    echo "   After opening your IDE, ask the AI to index your project:"
    echo ""
    echo -e "${YELLOW}   ┌─────────────────────────────────────────────────────────────┐${RESET}"
    echo -e "${YELLOW}   │ ${BLUE}Suggested AI Prompt:${YELLOW}                                     │${RESET}"
    echo -e "${YELLOW}   ├─────────────────────────────────────────────────────────────┤${RESET}"
    echo -e "${YELLOW}   │${RESET} Please use the CodeRAG MCP tool 'index_workspace' to      ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} index this project for semantic code search. Provide      ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} the file_path parameter pointing to any file in this      ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} workspace. Once indexing completes, I'll be able to use   ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} search_code, get_function_details, and other tools to     ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} help you navigate and understand the codebase.            ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET}                                                            ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} ${GREEN}Note:${RESET} Indexing runs in the background and may take a    ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} few minutes depending on project size. You can start      ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} using search immediately - results will improve as        ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   │${RESET} indexing progresses.                                      ${YELLOW}│${RESET}"
    echo -e "${YELLOW}   └─────────────────────────────────────────────────────────────┘${RESET}"
    echo ""
    echo -e "   ${BLUE}Repeat this for each project you want to work with.${RESET}"
    echo ""
    echo "📚 Documentation:"
    echo "   Quick Start: https://github.com/${REPO_SLUG}/blob/${BRANCH}/QUICKSTART.md"
    echo "   README:      https://github.com/${REPO_SLUG}/blob/${BRANCH}/README.md"
    echo -e "${BLUE}────────────────────────────────────────────${RESET}"
    echo ""
}

# Main installation flow
main() {
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════╗${RESET}"
    echo -e "${GREEN}║   CodeRAG MCP - Quick Installer       ║${RESET}"
    echo -e "${GREEN}╚════════════════════════════════════════╝${RESET}"
    echo ""
    log "Installing from branch: ${BLUE}${BRANCH}${RESET}"
    echo ""
    
    check_prerequisites
    install_coderag
    setup_path
    configure_mcp
    start_services
    show_summary
}

main "$@"
