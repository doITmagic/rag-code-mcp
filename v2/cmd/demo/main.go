package main

import (
	context "context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/internal/branchstate"
	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
	"github.com/doITmagic/rag-code-mcp/v2/internal/detector"
	"github.com/doITmagic/rag-code-mcp/v2/internal/registry"
	"github.com/doITmagic/rag-code-mcp/v2/internal/resolver"
)

func main() {
	workspaceRoot := flag.String("workspace", "", "path to the workspace root or file")
	registryFile := flag.String("registry", "", "path to registry file (optional)")
	mode := flag.String("mode", "file", "resolution mode: file or root")
	flag.Parse()

	if strings.TrimSpace(*workspaceRoot) == "" {
		log.Fatal("--workspace is required")
	}

	registryPath := *registryFile
	if registryPath == "" {
		registryPath = filepath.Join(os.TempDir(), "ragcode-v2-registry.json")
	}
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		log.Fatalf("create registry dir: %v", err)
	}

	reg, err := registry.New(registryPath)
	if err != nil {
		log.Fatalf("load registry: %v", err)
	}

	mgr := branchstate.NewManager(
		branchstate.WithCacheTTL(0),
		branchstate.WithLogger(consoleLogger{}),
	)

	deps := resolver.Dependencies{
		Detector:        detector.New(detector.DefaultOptions()),
		Registry:        &resolverRegistry{registry: reg},
		BranchAnnotator: &branchAnnotator{mgr: mgr},
		Logger:          consoleLogger{},
	}

	ctx := context.Background()
	var request contract.ResolveWorkspaceRequest
	switch *mode {
	case "file":
		request = contract.ResolveWorkspaceRequest{FilePath: *workspaceRoot}
	case "root":
		request = contract.ResolveWorkspaceRequest{WorkspaceRoot: *workspaceRoot}
	default:
		log.Fatalf("invalid mode %q: use 'file' or 'root'", *mode)
	}
	resp, resolveErr := resolver.New(deps).Resolve(ctx, request)
	if resolveErr != nil {
		log.Fatalf("resolve failed: %s (code=%s)", resolveErr.Message, resolveErr.Code)
	}

	fmt.Printf("Resolved Root: %s\n", resp.ResolvedRoot)
	fmt.Printf("Reason: %s\n", resp.Reason)
	fmt.Printf("Reindex Required: %t\n", resp.ReindexRequired)
	if resp.RequiresConfirmation {
		fmt.Println("Resolver requires confirmation due to multiple candidates:")
		for _, c := range resp.Candidates {
			fmt.Printf("  - %s\n", c.Root)
		}
	} else {
		if _, err := reg.Upsert(resp.ResolvedRoot, filepath.Base(resp.ResolvedRoot), "demo-client"); err != nil {
			log.Printf("warning: failed to persist registry entry: %v", err)
		}
	}
}

// consoleLogger provides simple stdout logging for demos.
type consoleLogger struct{}

func (consoleLogger) Debug(ctx context.Context, msg string, fields map[string]any) {
	log.Printf("%s %v", msg, fields)
}

// branchAnnotator adapts branchstate.Manager to resolver's Annotator interface.
type branchAnnotator struct {
	mgr *branchstate.Manager
}

func (a *branchAnnotator) Annotate(ctx context.Context, root string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError {
	_, reindex, reason, err := a.mgr.CompareAndUpdate(ctx, root)
	if err != nil {
		return err
	}
	resp.ReindexRequired = reindex
	if reason != "" {
		resp.Reason = reason
	}
	return nil
}

// resolverRegistry adapts registry.Registry to resolver.Registry.
type resolverRegistry struct {
	registry *registry.Registry
}

func (r *resolverRegistry) ResolveAlias(_ context.Context, alias string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	entries := r.registry.LookupByName(alias)
	if len(entries) == 0 {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorAmbiguousWorkspace,
			Message: fmt.Sprintf("workspace alias %q not found", alias),
			Reason:  contract.ReasonWorkspaceAlias,
		}
	}

	entry := entries[0]
	return &contract.WorkspaceCandidate{
		Root:   entry.Root,
		Name:   entry.Name,
		Reason: contract.ReasonWorkspaceAlias,
	}, nil
}

func (r *resolverRegistry) RecordFeedback(_ context.Context, feedback *contract.PathFeedback) error {
	return nil
}
