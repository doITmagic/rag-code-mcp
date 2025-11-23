package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

func main() {
	var (
		pathsCSV   = flag.String("paths", "", "Comma-separated list of directories to index for code (defaults to rag_code.paths)")
		model      = flag.String("model", "", "Embedding model id (overrides config; empty = use config)")
		codeColl   = flag.String("code-collection", "", "Qdrant collection name for code (default: rag_code.collection)")
		docsColl   = flag.String("docs-collection", "", "Qdrant collection name for docs (default: docs.collection)")
		dim        = flag.Int("dim", 768, "Vector dimension for collections (depends on model)")
		timeoutSec = flag.Int("timeout", 300, "Indexing timeout in seconds")
		configPath = flag.String("config", "config.yaml", "Path to config.yaml to read settings")
		sourceDocs = flag.String("docs-source", "docs", "Source tag for docs metadata")
		recreate   = flag.Bool("recreate-collections", false, "If set, delete and recreate code/docs collections before indexing (DANGEROUS)")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	codeCollection := cfg.RagCode.Collection
	if codeCollection == "" {
		if cfg.Storage.VectorDB.Collection != "" {
			codeCollection = cfg.Storage.VectorDB.Collection
		} else {
			codeCollection = "do-ai-code"
		}
	}
	if *codeColl != "" {
		codeCollection = *codeColl
	}

	docsCollection := cfg.Docs.Collection
	if *docsColl != "" {
		docsCollection = *docsColl
	}

	paths := cfg.RagCode.Paths
	if len(paths) == 0 {
		paths = []string{"./internal", "./cmd"}
	}
	if *pathsCSV != "" {
		paths = splitCSV(*pathsCSV)
	}

	llmCfg := cfg.LLM
	if llmCfg.OllamaBaseURL == "" && llmCfg.BaseURL != "" {
		llmCfg.OllamaBaseURL = llmCfg.BaseURL
	}
	if llmCfg.OllamaModel == "" && llmCfg.Model != "" {
		llmCfg.OllamaModel = llmCfg.Model
	}
	if *model != "" {
		llmCfg.OllamaEmbed = *model
	} else if cfg.RagCode.Model != "" {
		llmCfg.OllamaEmbed = cfg.RagCode.Model
	}
	if llmCfg.OllamaEmbed == "" {
		llmCfg.OllamaEmbed = cfg.LLM.OllamaEmbed
	}
	llmCfg.Provider = "ollama"

	provider, err := llm.NewOllamaLLMProvider(llmCfg)
	if err != nil {
		log.Fatalf("ollama provider: %v", err)
	}

	qcfgCode := storage.QdrantConfig{
		URL:        cfg.Storage.VectorDB.URL,
		APIKey:     cfg.Storage.VectorDB.APIKey,
		Collection: codeCollection,
	}
	// Wait for Qdrant gRPC to become available (default port 6334)
	if err := waitForQdrantGRPC(cfg.Storage.VectorDB.URL, 30*time.Second); err != nil {
		log.Fatalf("qdrant grpc port did not become available in time: %v", err)
	}

	qclientCode, err := storage.NewQdrantClient(qcfgCode)
	if err != nil {
		log.Fatalf("qdrant code client: %v", err)
	}
	defer qclientCode.Close()

	if *recreate {
		log.Printf("⚠️ Recreating code collection '%s'", codeCollection)
		if err := qclientCode.DeleteCollection(ctx, codeCollection); err != nil {
			log.Fatalf("delete code collection: %v", err)
		}
	}

	if err := qclientCode.CreateCollection(ctx, codeCollection, *dim); err != nil {
		log.Fatalf("create code collection: %v", err)
	}

	// Create workspace manager
	mgr := workspace.NewManager(qclientCode, provider, cfg)

	// Create workspace info manually
	info := &workspace.Info{
		ID:          fmt.Sprintf("cli-%s", codeCollection),
		Root:        filepath.Clean(paths[0]), // Use first path as root for state tracking
		ProjectType: "mixed",
		Languages:   []string{"go", "php"}, // We could detect this, but for now hardcode supported langs
	}

	// If multiple paths are provided, we might need a better strategy for Root.
	// For now, assume paths[0] is the workspace root.
	if len(paths) > 1 {
		log.Printf("⚠️ Multiple paths provided. Using '%s' as workspace root for state tracking.", info.Root)
	}

	// Index Go files
	fmt.Printf("🔎 Indexing Go files in '%s' (incremental)...\n", info.Root)
	if err := mgr.IndexLanguage(ctx, info, "go", codeCollection); err != nil {
		log.Printf("⚠️ Go indexing warning: %v", err)
	}

	// Index PHP files
	fmt.Printf("🔎 Indexing PHP files in '%s' (incremental)...\n", info.Root)
	if err := mgr.IndexLanguage(ctx, info, "php", codeCollection); err != nil {
		log.Printf("⚠️ PHP indexing warning: %v", err)
	}

	fmt.Println("✅ Code indexing completed.")

	var ltmDocs memory.LongTermMemory
	if docsCollection == "" {
		fmt.Println("ℹ️ docs.collection is empty, skipping docs indexing")
	} else {
		qcfgDocs := storage.QdrantConfig{
			URL:        cfg.Storage.VectorDB.URL,
			APIKey:     cfg.Storage.VectorDB.APIKey,
			Collection: docsCollection,
		}

		qclientDocs, err := storage.NewQdrantClient(qcfgDocs)
		if err != nil {
			log.Fatalf("qdrant docs client: %v", err)
		}
		defer qclientDocs.Close()

		if *recreate {
			log.Printf("⚠️ Recreating docs collection '%s'", docsCollection)
			if err := qclientDocs.DeleteCollection(ctx, docsCollection); err != nil {
				log.Fatalf("delete docs collection: %v", err)
			}
		}

		if err := qclientDocs.CreateCollection(ctx, docsCollection, *dim); err != nil {
			log.Fatalf("create docs collection: %v", err)
		}

		ltmDocs = storage.NewQdrantLongTermMemory(qclientDocs)
		var _ memory.LongTermMemory = ltmDocs

		readmePath := cfg.Docs.ReadmePath
		if readmePath == "" {
			readmePath = "./README.md"
		}

		docsPaths := cfg.Docs.DocsPaths
		if len(docsPaths) == 0 {
			docsPaths = []string{"./docs"}
		}

		var docFiles []string
		if fi, err := os.Stat(readmePath); err == nil && !fi.IsDir() {
			docFiles = append(docFiles, readmePath)
		}

		for _, root := range docsPaths {
			_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					return nil
				}
				if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
					docFiles = append(docFiles, path)
				}
				return nil
			})
		}

		if len(docFiles) == 0 {
			fmt.Println("ℹ️ no markdown files found for docs indexing")
		} else {
			fmt.Printf("📚 Indexing %d docs file(s) into docs collection '%s' (model=%s, dim=%d) ...\n", len(docFiles), docsCollection, llmCfg.OllamaEmbed, *dim)

			indexedDocs := 0
			for _, path := range docFiles {
				if err := indexMarkdownFile(ctx, provider, ltmDocs, path, *sourceDocs); err != nil {
					log.Fatalf("docs indexing failed for %s after %d file(s): %v", path, indexedDocs, err)
				}
				indexedDocs++
			}

			fmt.Printf("✅ Indexed %d docs file(s)\n", indexedDocs)
		}
	}

}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// waitForQdrantGRPC pings Qdrant gRPC port on the host inferred from the given REST URL.
// If the REST URL has port 6333, this function will try host:6334, which is Qdrant gRPC default.
func waitForQdrantGRPC(baseURL string, timeout time.Duration) error {
	if baseURL == "" {
		baseURL = "http://localhost:6333"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid qdrant url: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	var grpcHost string
	if port == "" || port == "6333" {
		grpcHost = net.JoinHostPort(host, "6334")
	} else {
		// If a non-standard port was specified, guess that gRPC is at same port or +1?
		// Use the same port by default.
		grpcHost = net.JoinHostPort(host, port)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", grpcHost, 2*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("timed out waiting for qdrant grpc at %s", grpcHost)
}

func indexMarkdownFile(ctx context.Context, provider llm.Provider, ltm memory.LongTermMemory, path string, source string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		chunks   []string
		current  strings.Builder
		maxChars = 1000
	)

	flushChunk := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			chunks = append(chunks, text)
		}
		current.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" && current.Len() > 0 {
			flushChunk()
			continue
		}

		if current.Len()+len(line)+1 > maxChars {
			flushChunk()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	flushChunk()

	for i, text := range chunks {
		emb, err := provider.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("embed failed for %s chunk %d: %w", path, i, err)
		}

		h := fnv.New64a()
		h.Write([]byte(fmt.Sprintf("%s#%d", path, i)))
		id := fmt.Sprintf("%d", h.Sum64())

		doc := memory.Document{
			ID:        id,
			Content:   text,
			Embedding: emb,
			Metadata: map[string]interface{}{
				"file":     path,
				"chunk_id": i,
				"source":   source,
			},
		}

		if err := ltm.Store(ctx, doc); err != nil {
			return fmt.Errorf("store failed for %s: %w", id, err)
		}
	}

	return nil
}
