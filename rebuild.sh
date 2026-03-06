#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAIN_GO="$ROOT_DIR/cmd/rag-code-mcp/main.go"
BIN_DIR="$ROOT_DIR/bin"
cv="$(grep -E '^\s*Version\s*=\s*"' "$MAIN_GO" | head -n1 | sed -E 's/.*"([^"]+)".*/\1/')"
[[ "$cv" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] || { echo "bad Version: $cv" >&2; exit 1; }
maj="${BASH_REMATCH[1]}"; min="${BASH_REMATCH[2]}"; pat=$((BASH_REMATCH[3]+1))
nv="$maj.$min.$pat"
perl -0777 -i -pe 's/(\bVersion\s*=\s*")([^"]+)(")/${1}'"$nv"'${3}/' "$MAIN_GO"
mkdir -p "$BIN_DIR"
# Kill running instances before overwriting binaries (dev convenience).
pkill -f rag-code-mcp || true
sleep 0.5
go build -o "$BIN_DIR/rag-code-mcp" "$ROOT_DIR/cmd/rag-code-mcp"
go build -o "$BIN_DIR/rag-code-install" "$ROOT_DIR/cmd/rag-code-install"
cp "$ROOT_DIR/internal/config/default.yaml" "$BIN_DIR/config.yaml"
echo "$cv -> $nv"

