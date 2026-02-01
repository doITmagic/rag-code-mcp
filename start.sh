#!/bin/bash
set -e

# CodeRAG MCP Server - Complete Setup and Start Script
# This script handles installation, dependency setup, and server startup

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${HOME}/.local/share/coderag"
BIN_DIR="${INSTALL_DIR}/bin"

echo ""
echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  CodeRAG MCP Server - Setup & Start${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
echo ""

# Build binaries and install to global directory
build_binaries() {
    echo -e "${BLUE}Checking binaries...${NC}"
    
    # Create installation directories
    mkdir -p "${BIN_DIR}"
    
    local need_build=false
    
    # Check if binaries exist in global location
    if [ ! -f "${BIN_DIR}/coderag-mcp" ]; then
        echo -e "${YELLOW}⚠ MCP server binary not found in ${BIN_DIR}${NC}"
        need_build=true
    fi
    
    if [ ! -f "${BIN_DIR}/index-all" ]; then
        echo -e "${YELLOW}⚠ Indexer binary not found in ${BIN_DIR}${NC}"
        need_build=true
    fi
    
    if [ "$need_build" = true ]; then
        echo -e "${BLUE}  Building binaries...${NC}"
        
        if ! command -v go &> /dev/null; then
            echo -e "${RED}✗ Go is not installed${NC}"
            echo -e "${BLUE}  Install Go: https://go.dev/doc/install${NC}"
            exit 1
        fi
        
        cd "$SCRIPT_DIR"
        
        # Build to temporary local bin directory
        mkdir -p bin
        
        if [ ! -f "${BIN_DIR}/coderag-mcp" ]; then
            echo -e "${BLUE}  Building coderag-mcp...${NC}"
            go build -o bin/coderag-mcp ./cmd/coderag-mcp
            cp bin/coderag-mcp "${BIN_DIR}/"
            echo -e "${GREEN}  ✓ Installed to ${BIN_DIR}/coderag-mcp${NC}"
        fi
        
        if [ ! -f "${BIN_DIR}/index-all" ]; then
            echo -e "${BLUE}  Building index-all...${NC}"
            go build -o bin/index-all ./cmd/index-all
            cp bin/index-all "${BIN_DIR}/"
            echo -e "${GREEN}  ✓ Installed to ${BIN_DIR}/index-all${NC}"
        fi
        
        echo -e "${GREEN}✓ Binaries built and installed successfully${NC}"
    else
        echo -e "${GREEN}✓ All binaries present in ${BIN_DIR}${NC}"
    fi
    
    echo ""
}

# Check if Docker is available
check_docker() {
    if ! command -v docker &> /dev/null; then
        echo -e "${RED}✗ Docker is not installed${NC}"
        echo -e "${BLUE}  Install Docker: https://docs.docker.com/get-docker/${NC}"
        return 1
    fi
    
    if ! docker info &> /dev/null; then
        echo -e "${RED}✗ Docker daemon is not running${NC}"
        echo -e "${BLUE}  Start Docker and try again${NC}"
        return 1
    fi
    
    echo -e "${GREEN}✓ Docker is available${NC}"
    return 0
}

# Start Qdrant container
start_qdrant() {
    echo ""
    echo -e "${BLUE}Starting Qdrant (global service)...${NC}"
    
    # Set global data directory
    QDRANT_DATA_DIR="${HOME}/.local/share/qdrant"
    
    # Check if Qdrant is already running
    if curl -s http://localhost:6333/readyz &> /dev/null; then
        echo -e "${GREEN}✓ Qdrant is already running${NC}"
        echo -e "${GREEN}  Data: ${QDRANT_DATA_DIR}${NC}"
        # Check if gRPC port is accessible
        if nc -z localhost 6334 2>/dev/null; then
            echo -e "${GREEN}  ✓ gRPC port (6334) is accessible${NC}"
            return 0
        else
            echo -e "${YELLOW}  ⚠ gRPC port (6334) not accessible, restarting container...${NC}"
            docker stop qdrant 2>/dev/null || true
            docker rm qdrant 2>/dev/null || true
        fi
    fi
    
    # Create global data directory
    mkdir -p "${QDRANT_DATA_DIR}"
    echo -e "${BLUE}  Using global data directory: ${QDRANT_DATA_DIR}${NC}"
    
    # Stop and remove existing container if any
    docker stop qdrant 2>/dev/null || true
    docker rm qdrant 2>/dev/null || true
    
    # Start new container with global volume
    if docker run -d --name qdrant \
        -p 6333:6333 \
        -p 6334:6334 \
        -v "${QDRANT_DATA_DIR}:/qdrant/storage" \
        qdrant/qdrant > /dev/null; then
        
        echo -e "${BLUE}  Waiting for Qdrant to start...${NC}"
        for i in {1..30}; do
            if curl -s http://localhost:6333/readyz &> /dev/null; then
                echo -e "${GREEN}✓ Qdrant started successfully${NC}"
                echo -e "${GREEN}  REST API: http://localhost:6333${NC}"
                echo -e "${GREEN}  gRPC API: localhost:6334${NC}"
                echo -e "${GREEN}  Data: ${QDRANT_DATA_DIR}${NC}"
                return 0
            fi
            sleep 1
        done
        
        echo -e "${RED}✗ Qdrant failed to start in time${NC}"
        return 1
    else
        echo -e "${RED}✗ Failed to start Qdrant container${NC}"
        return 1
    fi
}

# Check and start Ollama
check_ollama() {
    echo ""
    echo -e "${BLUE}Checking Ollama...${NC}"
    
    if ! command -v ollama &> /dev/null; then
        echo -e "${RED}✗ Ollama is not installed${NC}"
        echo -e "${BLUE}  Install: curl https://ollama.ai/install.sh | sh${NC}"
        return 1
    fi
    
    echo -e "${GREEN}✓ Ollama is installed${NC}"
    
    # Check if Ollama service is running
    if ! curl -s http://localhost:11434/api/tags &> /dev/null; then
        echo -e "${YELLOW}⚠ Ollama service is not running${NC}"
        echo -e "${BLUE}  Starting Ollama service in background...${NC}"
        
        # Try to start ollama serve in background
        nohup ollama serve > /dev/null 2>&1 &
        
        # Wait for service to be ready
        echo -e "${BLUE}  Waiting for Ollama to start...${NC}"
        for i in {1..30}; do
            if curl -s http://localhost:11434/api/tags &> /dev/null; then
                echo -e "${GREEN}✓ Ollama service started${NC}"
                break
            fi
            sleep 1
        done
        
        if ! curl -s http://localhost:11434/api/tags &> /dev/null; then
            echo -e "${RED}✗ Failed to start Ollama service${NC}"
            echo -e "${BLUE}  Start manually: ollama serve${NC}"
            return 1
        fi
    fi
    
    # Check for required model
    if ! ollama list 2>/dev/null | grep -q "nomic-embed-text"; then
        echo -e "${YELLOW}⚠ nomic-embed-text model not found${NC}"
        echo -e "${BLUE}  Pulling model (this may take a few minutes)...${NC}"
        
        if timeout 300 ollama pull nomic-embed-text; then
            echo -e "${GREEN}✓ Model downloaded successfully${NC}"
        else
            echo -e "${RED}✗ Failed to download model${NC}"
            echo -e "${BLUE}  Try manually: ollama pull nomic-embed-text${NC}"
            return 1
        fi
    else
        echo -e "${GREEN}✓ nomic-embed-text model is available${NC}"
    fi
    
    return 0
}

# Note: Indexing is now done per-workspace automatically when first accessed
# No need to index here - the MCP server will detect and index workspaces on-demand

# Start MCP server
start_mcp_server() {
    echo ""
    echo -e "${BLUE}Starting MCP server...${NC}"
    
    echo -e "${GREEN}✓ Ready to start server${NC}"
    echo ""
    echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  🎉 All services are running!${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "${BLUE}  Installation: ${INSTALL_DIR}${NC}"
    echo -e "${BLUE}  Config will be auto-created on first run${NC}"
    echo -e "${BLUE}  MCP Server starting in stdio mode...${NC}"
    echo -e "${BLUE}  Press Ctrl+C to stop${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
    echo ""
    
    cd "${INSTALL_DIR}"
    exec "${BIN_DIR}/coderag-mcp" -config config.yaml
}

# Main flow
main() {
    build_binaries
    
    if ! check_docker; then
        echo ""
        echo -e "${YELLOW}Docker is recommended but not required${NC}"
        echo -e "${BLUE}You can run Qdrant manually or use docker-compose${NC}"
    else
        if ! start_qdrant; then
            echo -e "${RED}Failed to start Qdrant${NC}"
            exit 1
        fi
    fi
    
    if ! check_ollama; then
        echo ""
        echo -e "${RED}Ollama is required. Please install it and try again.${NC}"
        echo -e "${BLUE}Install: curl https://ollama.ai/install.sh | sh${NC}"
        exit 1
    fi
    
    # Note: Indexing is now automatic per-workspace when first tool is called
    echo ""
    echo -e "${BLUE}ℹ Multi-workspace mode enabled${NC}"
    echo -e "${BLUE}  Workspaces will be auto-indexed on first use${NC}"
    echo -e "${BLUE}  Data location: ~/.local/share/qdrant/${NC}"

    if [ "${CODERAG_SKIP_SERVER_START:-0}" = "1" ]; then
        echo ""
        echo -e "${YELLOW}Setup complete. CODERAG_SKIP_SERVER_START=1 so the MCP server was not started.${NC}"
        echo -e "${YELLOW}Run ${INSTALL_DIR}/start.sh without this flag to launch the server.${NC}"
        return 0
    fi

    start_mcp_server
}

main
