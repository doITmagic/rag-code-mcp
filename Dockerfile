# Multi-stage Dockerfile for RagCode MCP Server
# Optimized for small image size and security

FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies (cached layer)
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build with optimizations and version info
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.Date=${DATE}" \
    -o /rag-code-mcp \
    ./cmd/rag-code-mcp

# Final stage - minimal image
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 mcp && \
    adduser -D -u 1000 -G mcp mcp

# Copy binary from builder
COPY --from=builder /rag-code-mcp /usr/local/bin/rag-code-mcp

# Copy example config
COPY config.yaml /etc/ragcode/config.yaml.example

# Default environment variables
ENV OLLAMA_BASE_URL="http://host.docker.internal:11434" \
    OLLAMA_MODEL="phi3:medium" \
    OLLAMA_EMBED="nomic-embed-text" \
    QDRANT_URL="http://host.docker.internal:6333" \
    QDRANT_COLLECTION="do-ai-code" \
    MCP_LOG_LEVEL="info"

# Switch to non-root user
USER mcp

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/rag-code-mcp", "--version"] || exit 1

ENTRYPOINT ["/usr/local/bin/rag-code-mcp"]
