package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/internal/branchstate"
	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
	"github.com/doITmagic/rag-code-mcp/v2/internal/detector"
	"github.com/doITmagic/rag-code-mcp/v2/internal/service/indexer"
	"github.com/doITmagic/rag-code-mcp/v2/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

// Engine is the high-level orchestrator for RAG operations.
type Engine struct {
	indexer       *indexer.Service
	search        *search.Service
	detector      *detector.Detector
	branchManager *branchstate.Manager
}

// NewEngine creates a new Engine instance.
func NewEngine(idx *indexer.Service, srv *search.Service) *Engine {
	return &Engine{
		indexer:       idx,
		search:        srv,
		detector:      detector.New(detector.DefaultOptions()),
		branchManager: branchstate.NewManager(),
	}
}

// WorkspaceContext provides information about a detected workspace.
type WorkspaceContext struct {
	Root       string
	ID         string
	Branch     string
	WorktreeID string
}

// DetectContext detects the workspace context for a given path.
func (e *Engine) DetectContext(ctx context.Context, path string) (*WorkspaceContext, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path is empty")
	}

	root, err := e.detectRoot(ctx, path)
	if err != nil {
		return nil, err
	}

	branch := "main"
	worktreeID := root
	if e.branchManager != nil {
		state, _, _, stateErr := e.branchManager.CompareAndUpdate(ctx, root)
		if stateErr == nil && state != nil {
			if strings.TrimSpace(state.LastBranch) != "" {
				branch = state.LastBranch
			}
			if strings.TrimSpace(state.LastWorktreeID) != "" {
				worktreeID = state.LastWorktreeID
			}
		}
	}

	id := contract.DeriveWorkspaceID(root, branch, worktreeID)

	return &WorkspaceContext{
		Root:       root,
		ID:         id,
		Branch:     branch,
		WorktreeID: worktreeID,
	}, nil
}

func (e *Engine) detectRoot(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	if e.detector != nil {
		candidate, detErr := e.detector.DetectFromFilePath(ctx, abs)
		if detErr == nil && candidate != nil && strings.TrimSpace(candidate.Root) != "" {
			return candidate.Root, nil
		}
	}

	info, statErr := os.Stat(abs)
	if statErr != nil {
		return "", fmt.Errorf("failed to stat path: %w", statErr)
	}
	if info.IsDir() {
		return abs, nil
	}
	return filepath.Dir(abs), nil
}

// IndexWorkspace indexes all files in a workspace.
func (e *Engine) IndexWorkspace(ctx context.Context, path string) error {
	wctx, err := e.DetectContext(ctx, path)
	if err != nil {
		return err
	}

	// Walk and group symbols
	langSymbols := make(map[string][]parser.Symbol)

	err = filepath.WalkDir(wctx.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip hidden dirs (like .git, .vendor, .venv)
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules" || d.Name() == "venv" {
				return filepath.SkipDir
			}
			return nil
		}

		analyzer := parser.GetByFile(p)
		if analyzer == nil {
			return nil
		}

		res, err := analyzer.Analyze(ctx, p)
		if err != nil {
			return nil
		}

		langSymbols[analyzer.Name()] = append(langSymbols[analyzer.Name()], res.Symbols...)
		return nil
	})

	if err != nil {
		return err
	}

	for lang, symbols := range langSymbols {
		collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
		// Add branch to metadata for each symbol before indexing
		for i := range symbols {
			if symbols[i].Metadata == nil {
				symbols[i].Metadata = make(map[string]any)
			}
			symbols[i].Metadata["branch"] = wctx.Branch
		}

		if err := e.indexer.IndexItems(ctx, collection, symbols); err != nil {
			return fmt.Errorf("failed to index %s: %w", lang, err)
		}
	}

	return nil
}

// Query performs a search across common language collections in a workspace.
func (e *Engine) Query(ctx context.Context, path string, queryText string, limit int) ([]any, error) {
	wctx, err := e.DetectContext(ctx, path)
	if err != nil {
		return nil, err
	}

	langs := []string{"go", "php", "python"}
	var allResults []any

	for _, lang := range langs {
		collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
		res, err := e.search.Search(ctx, collection, queryText, limit)
		if err != nil {
			continue // Collection might not exist
		}
		for _, r := range res {
			allResults = append(allResults, r)
		}
	}

	return allResults, nil
}
