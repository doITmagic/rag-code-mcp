#!/usr/bin/env bash
set -euo pipefail

CONTAINER_IMAGE_DEFAULT="ubuntu:22.04"
CONTAINER_NAME_DEFAULT=""
MCP_PORT_DEFAULT="8080"

BASE_NAME_DEFAULT="ragcode-e2e-base"
SNAPSHOT_NAME_DEFAULT="clean"
SCENARIO_DEFAULT="clean_docker"

REPO_URL_DEFAULT="https://github.com/doITmagic/rag-code-mcp.git"
REPO_DIR_DEFAULT="/tmp/rag-code-mcp"
REPO_REF_DEFAULT=""

BIN_DIR_DEFAULT=""

SSE_FILE_DEFAULT="README.md"
SSE_QUERY_DEFAULT="IndexWorkspace implementation"
SSE_LIMIT_DEFAULT="3"
SSE_MODE_DEFAULT="exact"

usage() {
  echo "Usage: $0 -scenario <name> [options]"
  echo ""
  echo "Scenarios:"
  echo "  clean_docker                 Clean install using Docker for Ollama+Qdrant and run SSE E2E"
  echo "  reinstall_docker             Run installer twice (idempotency) and run SSE E2E"
  echo "  uninstall                    Install then uninstall (sanity)"
  echo "  docker_missing               Negative: no Docker in container (expected failure)"
  echo "  ollama_local_running         Ollama already running on 11434 (docker prestarted), installer uses -ollama local"
  echo "  ollama_local_missing_fallback  Ollama not running, installer starts Ollama via Docker after prompt (auto-yes)"
  echo ""
  echo "Options:"
  echo "  -scenario  Scenario name (default: ${SCENARIO_DEFAULT})"
  echo "  -i         LXC image for base creation (default: ${CONTAINER_IMAGE_DEFAULT})"
  echo "  -base      Base container name (default: ${BASE_NAME_DEFAULT})"
  echo "  -snap      Snapshot name on base (default: ${SNAPSHOT_NAME_DEFAULT})"
  echo "  -n         Test container name (default: auto)"
  echo "  -p         MCP SSE port inside container (default: ${MCP_PORT_DEFAULT})"
  echo "  -repo-url  Git repo to clone inside container (default: ${REPO_URL_DEFAULT})"
  echo "  -repo-dir  Target directory for clone inside container (default: ${REPO_DIR_DEFAULT})"
  echo "  -repo-ref  Git ref to checkout inside container after clone (default: empty)"
  echo "  -bin-dir   Directory with prebuilt binaries: rag-code-install, rag-code-mcp, mcp-http-client-test (default: build locally)"
  echo "  -sse-file  File inside repo used as file_path (default: ${SSE_FILE_DEFAULT})"
  echo "  -sse-query Query for rag_search_code (default: ${SSE_QUERY_DEFAULT})"
  echo "  -sse-limit Limit for rag_search_code (default: ${SSE_LIMIT_DEFAULT})"
  echo "  -sse-mode  rag_search_code mode: discovery or exact (default: ${SSE_MODE_DEFAULT})"
  echo ""
  echo "Examples:"
  echo "  $0 -scenario clean_docker"
  echo "  $0 -scenario ollama_local_running"
}

IMAGE="${CONTAINER_IMAGE_DEFAULT}"
BASE_NAME="${BASE_NAME_DEFAULT}"
SNAPSHOT_NAME="${SNAPSHOT_NAME_DEFAULT}"
SCENARIO="${SCENARIO_DEFAULT}"
NAME="${CONTAINER_NAME_DEFAULT}"
PORT="${MCP_PORT_DEFAULT}"
REPO_URL="${REPO_URL_DEFAULT}"
REPO_DIR="${REPO_DIR_DEFAULT}"
REPO_REF="${REPO_REF_DEFAULT}"

BIN_DIR="${BIN_DIR_DEFAULT}"

SSE_FILE="${SSE_FILE_DEFAULT}"
SSE_QUERY="${SSE_QUERY_DEFAULT}"
SSE_LIMIT="${SSE_LIMIT_DEFAULT}"
SSE_MODE="${SSE_MODE_DEFAULT}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -scenario)
      SCENARIO="$2"; shift 2 ;;
    -i)
      IMAGE="$2"; shift 2 ;;
    -base)
      BASE_NAME="$2"; shift 2 ;;
    -snap)
      SNAPSHOT_NAME="$2"; shift 2 ;;
    -n)
      NAME="$2"; shift 2 ;;
    -p)
      PORT="$2"; shift 2 ;;
    -repo-url)
      REPO_URL="$2"; shift 2 ;;
    -repo-dir)
      REPO_DIR="$2"; shift 2 ;;
    -repo-ref)
      REPO_REF="$2"; shift 2 ;;
    -bin-dir)
      BIN_DIR="$2"; shift 2 ;;
    -sse-file)
      SSE_FILE="$2"; shift 2 ;;
    -sse-query)
      SSE_QUERY="$2"; shift 2 ;;
    -sse-limit)
      SSE_LIMIT="$2"; shift 2 ;;
    -sse-mode)
      SSE_MODE="$2"; shift 2 ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown arg: $1" >&2
      usage
      exit 2
      ;;
  esac

done

if ! command -v lxc >/dev/null 2>&1; then
  echo "ERROR: 'lxc' not found. Install LXD/Incus client first." >&2
  exit 1
fi

if [[ -z "${NAME}" ]]; then
  NAME="ragcode-e2e-${SCENARIO}-$(date +%s)"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_DIR="${REPO_ROOT}/tests/e2e/bin"

mkdir -p "${BUILD_DIR}"

cleanup() {
  set +e
  if lxc info "${NAME}" >/dev/null 2>&1; then
    lxc stop "${NAME}" --force >/dev/null 2>&1 || true
    lxc delete "${NAME}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

ensure_base_snapshot() {
  if ! lxc info "${BASE_NAME}" >/dev/null 2>&1; then
    echo "==> LXC: creating base container ${BASE_NAME} (${IMAGE})"
    lxc launch "${IMAGE}" "${BASE_NAME}" -c security.nesting=true
    echo "==> LXC(base): wait for cloud-init"
    lxc exec "${BASE_NAME}" -- bash -lc 'command -v cloud-init >/dev/null 2>&1 && cloud-init status --wait || true'
    echo "==> LXC(base): install deps (docker, git, curl)"
    lxc exec "${BASE_NAME}" -- bash -lc 'apt-get update && apt-get install -y docker.io git curl'
    echo "==> LXC(base): start docker"
    lxc exec "${BASE_NAME}" -- bash -lc 'systemctl enable docker >/dev/null 2>&1 || true; systemctl start docker'
  fi

  if ! lxc info "${BASE_NAME}/${SNAPSHOT_NAME}" >/dev/null 2>&1; then
    echo "==> LXC: creating snapshot ${BASE_NAME}/${SNAPSHOT_NAME}"
    lxc snapshot "${BASE_NAME}" "${SNAPSHOT_NAME}"
  fi
}

create_test_container_from_snapshot() {
  echo "==> LXC: create test container ${NAME} from ${BASE_NAME}/${SNAPSHOT_NAME}"
  lxc copy "${BASE_NAME}/${SNAPSHOT_NAME}" "${NAME}"
  lxc start "${NAME}"

  echo "==> LXC: wait for cloud-init"
  lxc exec "${NAME}" -- bash -lc 'command -v cloud-init >/dev/null 2>&1 && cloud-init status --wait || true'
}

build_binaries() {
  if [[ -n "${BIN_DIR}" ]]; then
    BUILD_DIR="${BIN_DIR}"
    echo "==> Build: skipped (using -bin-dir ${BUILD_DIR})"
    return
  fi

  echo "==> Build: rag-code-install"
  go build -o "${BUILD_DIR}/rag-code-install" "${REPO_ROOT}/cmd/rag-code-install"

  echo "==> Build: rag-code-mcp"
  go build -o "${BUILD_DIR}/rag-code-mcp" "${REPO_ROOT}/cmd/rag-code-mcp"

  echo "==> Build: mcp-http-client-test"
  go build -o "${BUILD_DIR}/mcp-http-client-test" "${REPO_ROOT}/cmd/mcp-http-client-test"
}

push_binaries() {
  echo "==> LXC: push binaries"
  lxc file push "${BUILD_DIR}/rag-code-install" "${NAME}/root/rag-code-install"
  lxc file push "${BUILD_DIR}/rag-code-mcp" "${NAME}/root/rag-code-mcp"
  lxc file push "${BUILD_DIR}/mcp-http-client-test" "${NAME}/root/mcp-http-client-test"
  lxc exec "${NAME}" -- bash -lc 'chmod +x /root/rag-code-install /root/rag-code-mcp /root/mcp-http-client-test'
}

start_mcp_and_wait() {
  echo "==> LXC: start MCP server (SSE)"
  lxc exec "${NAME}" -- bash -lc "mkdir -p /root/.local/share/ragcode/bin"
  lxc exec "${NAME}" -- bash -lc "cp /root/rag-code-mcp /root/.local/share/ragcode/bin/rag-code-mcp"
  lxc exec "${NAME}" -- bash -lc "nohup /root/.local/share/ragcode/bin/rag-code-mcp --transport sse --http-port ${PORT} >/root/mcp.log 2>&1 &"

  echo "==> LXC: wait MCP to answer"
  # There is no /health endpoint; readiness is verified by checking /sse responds with 200
  lxc exec "${NAME}" -- bash -lc "for i in {1..30}; do curl -sf -H 'Accept: text/event-stream' --max-time 2 http://127.0.0.1:${PORT}/sse >/dev/null && exit 0; sleep 1; done; echo 'MCP not responding on /sse'; tail -n 200 /root/mcp.log; exit 1"
}

run_sse_e2e() {
  echo "==> LXC: clone test repo"
  lxc exec "${NAME}" -- bash -lc "rm -rf '${REPO_DIR}' && git clone --depth 1 '${REPO_URL}' '${REPO_DIR}'"
  if [[ -n "${REPO_REF}" ]]; then
    lxc exec "${NAME}" -- bash -lc "cd '${REPO_DIR}' && git fetch --tags --force >/dev/null 2>&1 || true; git checkout -f '${REPO_REF}'"
  fi

  echo "==> LXC: run SSE client E2E"
  if [[ "${SSE_EMIT_JSON:-}" == "1" ]]; then
    lxc exec "${NAME}" -- bash -lc "/root/mcp-http-client-test -url http://127.0.0.1:${PORT} -path '${REPO_DIR}/${SSE_FILE}' -query '${SSE_QUERY}' -mode '${SSE_MODE}' -limit ${SSE_LIMIT} -out /root/sse_out.json"
    echo "@@@SSE_OUT_BEGIN@@@"
    lxc exec "${NAME}" -- bash -lc "cat /root/sse_out.json"
    echo "@@@SSE_OUT_END@@@"
  else
    lxc exec "${NAME}" -- bash -lc "/root/mcp-http-client-test -url http://127.0.0.1:${PORT} -path '${REPO_DIR}/${SSE_FILE}' -query '${SSE_QUERY}' -mode '${SSE_MODE}' -limit ${SSE_LIMIT}"
  fi
}

docker_cleanup_ragcode_services() {
  lxc exec "${NAME}" -- bash -lc 'docker rm -f ragcode-ollama ragcode-qdrant >/dev/null 2>&1 || true'
}

start_ollama_docker_only() {
  # Start only Ollama container as a "local" service provider on 11434.
  # We also pre-pull the image to reduce flakiness.
  lxc exec "${NAME}" -- bash -lc 'docker pull ollama/ollama >/dev/null'
  lxc exec "${NAME}" -- bash -lc 'docker run -d --name ragcode-ollama --restart always -p 11434:11434 -v ollama-data:/root/.ollama ollama/ollama >/dev/null'
  lxc exec "${NAME}" -- bash -lc 'for i in {1..30}; do (echo >/dev/tcp/127.0.0.1/11434) >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Ollama port not open"; docker logs ragcode-ollama | tail -n 200; exit 1'
}

scenario_clean_docker() {
  echo "==> Scenario: clean_docker"
  docker_cleanup_ragcode_services
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama docker -qdrant docker'
  start_mcp_and_wait
  run_sse_e2e
}

scenario_reinstall_docker() {
  echo "==> Scenario: reinstall_docker"
  docker_cleanup_ragcode_services
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama docker -qdrant docker'
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama docker -qdrant docker'
  start_mcp_and_wait
  run_sse_e2e
}

scenario_uninstall() {
  echo "==> Scenario: uninstall"
  docker_cleanup_ragcode_services
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama docker -qdrant docker'
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -uninstall'
  lxc exec "${NAME}" -- bash -lc 'test ! -d /root/.local/share/ragcode'
}

scenario_docker_missing() {
  echo "==> Scenario: docker_missing (expected failure)"
  # This scenario does NOT use the base snapshot on purpose.
  # It validates the installer behavior when docker is not available.
  lxc exec "${NAME}" -- bash -lc 'apt-get update && apt-get install -y git curl'

  set +e
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama docker -qdrant docker'
  rc=$?
  set -e

  if [[ $rc -eq 0 ]]; then
    echo "ERROR: docker_missing scenario expected installer to fail (docker not installed), but it exited 0" >&2
    exit 1
  fi

  echo "==> OK: installer failed as expected (rc=${rc})"
}

scenario_ollama_local_running() {
  echo "==> Scenario: ollama_local_running"
  docker_cleanup_ragcode_services
  start_ollama_docker_only

  # We expect installer to keep ollamaMode=local because port 11434 is already open.
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama local -qdrant docker'

  start_mcp_and_wait
  run_sse_e2e
}

scenario_ollama_local_missing_fallback() {
  echo "==> Scenario: ollama_local_missing_fallback"
  docker_cleanup_ragcode_services

  # No local service on 11434. Installer should prompt for starting docker, and with -y it will auto-confirm.
  lxc exec "${NAME}" -- bash -lc '/root/rag-code-install -y -ollama local -qdrant docker'

  start_mcp_and_wait
  run_sse_e2e
}

case "${SCENARIO}" in
  clean_docker|reinstall_docker|uninstall|ollama_local_running|ollama_local_missing_fallback)
    ensure_base_snapshot
    build_binaries
    create_test_container_from_snapshot
    push_binaries
    ;;
  docker_missing)
    build_binaries
    echo "==> LXC: launch ${NAME} (${IMAGE})"
    lxc launch "${IMAGE}" "${NAME}" -c security.nesting=true
    echo "==> LXC: wait for cloud-init"
    lxc exec "${NAME}" -- bash -lc 'command -v cloud-init >/dev/null 2>&1 && cloud-init status --wait || true'
    push_binaries
    ;;
  *)
    echo "Unknown scenario: ${SCENARIO}" >&2
    usage
    exit 2
    ;;
esac

case "${SCENARIO}" in
  clean_docker) scenario_clean_docker ;;
  reinstall_docker) scenario_reinstall_docker ;;
  uninstall) scenario_uninstall ;;
  docker_missing) scenario_docker_missing ;;
  ollama_local_running) scenario_ollama_local_running ;;
  ollama_local_missing_fallback) scenario_ollama_local_missing_fallback ;;
  *)
    echo "Unknown scenario: ${SCENARIO}" >&2
    usage
    exit 2
    ;;
esac

echo "==> OK: ${SCENARIO} passed"
