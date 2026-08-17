package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go" // register Go analyzer
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	. "github.com/onsi/gomega"
)

type mockEmbedder struct {
	embedCount int32
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	atomic.AddInt32(&m.embedCount, 1)
	return make([]float64, 1024), nil
}
func (m *mockEmbedder) Generate(ctx context.Context, prompt string, opts ...llm.GenerateOption) (string, error) {
	return "", nil
}
func (m *mockEmbedder) GenerateStream(ctx context.Context, prompt string, opts ...llm.GenerateOption) (<-chan string, <-chan error) {
	return nil, nil
}
func (m *mockEmbedder) Name() string                  { return "mock" }
func (m *mockEmbedder) GetEmbeddingDimension() uint64 { return 1024 }

type mockStore struct {
	storage.VectorStore
	upsertPoints    []storage.Point
	deletedPrefixes []string
	deletedFilters  []string
	mu              sync.Mutex
}

func (m *mockStore) Upsert(ctx context.Context, collection string, points []storage.Point) (*storage.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertPoints = append(m.upsertPoints, points...)
	return nil, nil
}

func (m *mockStore) CollectionExists(ctx context.Context, collection string) (bool, error) {
	return true, nil
}

func (m *mockStore) CreateCollection(ctx context.Context, collection string, dimension int) error {
	return nil
}

func (m *mockStore) DeleteByFilter(ctx context.Context, collection string, field string, value interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedFilters = append(m.deletedFilters, fmt.Sprintf("%v", value))
	return nil
}

func (m *mockStore) DeleteByPrefix(ctx context.Context, collection string, key string, prefix string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedPrefixes = append(m.deletedPrefixes, prefix)
	return 1, nil
}

func (m *mockStore) DeleteCollection(ctx context.Context, collection string) error {
	return nil
}

func TestCleanupStaleFiles(t *testing.T) {
	wsRoot := t.TempDir()
	srcDir := filepath.Join(wsRoot, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existingFile := filepath.Join(srcDir, "active.go")
	if err := os.WriteFile(existingFile, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := NewState()
	info, _ := os.Stat(existingFile)
	state.UpdateFile(existingFile, info)
	state.Files[filepath.Join(srcDir, "deleted_file.go")] = FileState{Path: filepath.Join(srcDir, "deleted_file.go")}
	state.Files[filepath.Join(wsRoot, "tmp", "sub", "orphan.go")] = FileState{Path: filepath.Join(wsRoot, "tmp", "sub", "orphan.go")}

	mockStore := &mockStore{}
	svc := NewService(&mockEmbedder{}, mockStore)

	cleaned := svc.cleanupStaleFiles(context.Background(), wsRoot, "test_collection", state, "go")
	if !cleaned {
		t.Fatal("expected cleanupStaleFiles to return true")
	}

	mockStore.mu.Lock()
	prefixes := mockStore.deletedPrefixes
	filters := mockStore.deletedFilters
	mockStore.mu.Unlock()

	expectedPrefix := filepath.Join(wsRoot, "tmp") + string(os.PathSeparator)
	if len(prefixes) != 1 || prefixes[0] != expectedPrefix {
		t.Errorf("expected deletedPrefixes [%q], got %v", expectedPrefix, prefixes)
	}

	expectedFilter := filepath.Join(srcDir, "deleted_file.go")
	if len(filters) != 1 || filters[0] != expectedFilter {
		t.Errorf("expected deletedFilters [%q], got %v", expectedFilter, filters)
	}

	state.mu.RLock()
	remaining := len(state.Files)
	_, activeExists := state.Files[existingFile]
	state.mu.RUnlock()

	if remaining != 1 || !activeExists {
		t.Errorf("expected 1 remaining file in state (active.go), got %d files", remaining)
	}
}

type mockStoreDeleteRecreate struct {
	storage.VectorStore

	mu                  sync.Mutex
	deleteCalls         int
	existsCalls         int
	existsUntilDeletes  int
	deleteErrUntilCalls int
	collectionExistsErr error
}

func (m *mockStoreDeleteRecreate) DeleteCollection(ctx context.Context, collection string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	if m.deleteCalls <= m.deleteErrUntilCalls {
		return errors.New("delete failed")
	}
	return nil
}

func (m *mockStoreDeleteRecreate) CollectionExists(ctx context.Context, collection string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.existsCalls++
	if m.collectionExistsErr != nil {
		return false, m.collectionExistsErr
	}
	return m.deleteCalls < m.existsUntilDeletes, nil
}

func TestDeleteCollectionForRecreate_RetriesUntilGone(t *testing.T) {
	RegisterTestingT(t)

	mockEmbed := &mockEmbedder{}
	mockS := &mockStoreDeleteRecreate{
		existsUntilDeletes:  3,
		deleteErrUntilCalls: 2,
	}
	svc := NewService(mockEmbed, mockS)

	err := svc.deleteCollectionForRecreate(context.Background(), "col")
	Expect(err).NotTo(HaveOccurred())

	mockS.mu.Lock()
	deletes := mockS.deleteCalls
	existsChecks := mockS.existsCalls
	mockS.mu.Unlock()

	Expect(deletes).To(BeNumerically(">=", 3))
	Expect(existsChecks).To(BeNumerically(">=", 3))
}

func TestDeleteCollectionForRecreate_CollectionExistsError(t *testing.T) {
	RegisterTestingT(t)

	mockEmbed := &mockEmbedder{}
	mockS := &mockStoreDeleteRecreate{
		collectionExistsErr: errors.New("exists failed"),
	}
	svc := NewService(mockEmbed, mockS)

	err := svc.deleteCollectionForRecreate(context.Background(), "col")
	Expect(err).To(HaveOccurred())
}

func TestIndexItemsParallelism(t *testing.T) {
	RegisterTestingT(t)
	mockEmbed := &mockEmbedder{}
	mockS := &mockStore{}
	svc := NewService(mockEmbed, mockS)

	symbols := make([]parser.Symbol, 100)
	for i := 0; i < 100; i++ {
		symbols[i] = parser.Symbol{
			Name:      fmt.Sprintf("Sym%d", i),
			Content:   "content",
			FilePath:  "file.go",
			StartLine: i,
		}
	}

	err := svc.IndexItems(context.Background(), "test-col", symbols)
	Expect(err).NotTo(HaveOccurred())

	// Verify all 100 symbols were embedded
	Expect(atomic.LoadInt32(&mockEmbed.embedCount)).To(Equal(int32(100)))

	// Verify all symbols reached the store
	Expect(len(mockS.upsertPoints)).To(Equal(100))
}

// TestIndexWorkspaceSequentialFiles verifies that multiple files are indexed correctly
// in a sequential for loop. Run with -race to detect races on shared state.
func TestIndexWorkspaceSequentialFiles(t *testing.T) {
	RegisterTestingT(t)

	// One file per sub-package directory so each IndexFile call yields exactly 1 symbol.
	dir := t.TempDir()
	const numFiles = 8
	for i := 0; i < numFiles; i++ {
		pkgDir := filepath.Join(dir, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(pkgDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		src := fmt.Sprintf("package pkg%d\n\n// Func%d does something.\nfunc Func%d() int { return %d }\n", i, i, i, i)
		if err := os.WriteFile(filepath.Join(pkgDir, "code.go"), []byte(src), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	mockEmbed := &mockEmbedder{}
	mockS := &mockStore{}
	svc := NewService(mockEmbed, mockS)

	err := svc.IndexWorkspace(context.Background(), dir, "test-sequential", Options{Language: "go"})
	Expect(err).NotTo(HaveOccurred())

	// Each sub-package has 1 function → numFiles symbols total
	mockS.mu.Lock()
	total := len(mockS.upsertPoints)
	mockS.mu.Unlock()

	Expect(total).To(Equal(numFiles))
	Expect(int(atomic.LoadInt32(&mockEmbed.embedCount))).To(BeNumerically(">=", numFiles))
}

// TestIndexWorkspaceProgressIsAscending verifies that Progress callback is called
// in strictly ascending order — guaranteed by the sequential for loop.
func TestIndexWorkspaceProgressIsAscending(t *testing.T) {
	RegisterTestingT(t)

	dir := t.TempDir()
	const numFiles = 5
	for i := 0; i < numFiles; i++ {
		pkgDir := filepath.Join(dir, fmt.Sprintf("ppkg%d", i))
		Expect(os.MkdirAll(pkgDir, 0755)).To(Succeed())
		src := fmt.Sprintf("package ppkg%d\n\nfunc PF%d() {}\n", i, i)
		Expect(os.WriteFile(filepath.Join(pkgDir, "f.go"), []byte(src), 0644)).To(Succeed())
	}

	var calls []int
	var mu sync.Mutex

	mockEmbed := &mockEmbedder{}
	mockS := &mockStore{}
	svc := NewService(mockEmbed, mockS)

	err := svc.IndexWorkspace(context.Background(), dir, "test-ascending", Options{
		Language: "go",
		Progress: func(done, total int) {
			mu.Lock()
			calls = append(calls, done)
			mu.Unlock()
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(len(calls)).To(Equal(numFiles))

	// Sequential: each call must increment by exactly 1
	for i := 1; i < len(calls); i++ {
		Expect(calls[i]).To(Equal(calls[i-1]+1), "progress calls must be strictly ascending")
	}
}

// TestIndexWorkspaceNoDeadlock runs IndexWorkspace with many files and a tight timeout.
// With the sequential model there is no semaphore to deadlock, but this verifies
// that IndexWorkspace completes within a reasonable time.
func TestIndexWorkspaceNoDeadlock(t *testing.T) {
	RegisterTestingT(t)

	dir := t.TempDir()
	const numFiles = 20
	for i := 0; i < numFiles; i++ {
		pkgDir := filepath.Join(dir, fmt.Sprintf("dpkg%d", i))
		Expect(os.MkdirAll(pkgDir, 0755)).To(Succeed())
		src := fmt.Sprintf("package dpkg%d\n\nfunc F%d() {}\n", i, i)
		Expect(os.WriteFile(filepath.Join(pkgDir, "f.go"), []byte(src), 0644)).To(Succeed())
	}

	mockEmbed := &mockEmbedder{}
	mockS := &mockStore{}
	svc := NewService(mockEmbed, mockS)

	done := make(chan error, 1)
	go func() {
		done <- svc.IndexWorkspace(context.Background(), dir, "deadlock-test", Options{
			Language: "go",
		})
	}()

	select {
	case err := <-done:
		Expect(err).NotTo(HaveOccurred())
	case <-time.After(30 * time.Second):
		t.Fatal("IndexWorkspace took too long — possible infinite loop or deadlock")
	}

	mockS.mu.Lock()
	total := len(mockS.upsertPoints)
	mockS.mu.Unlock()
	Expect(total).To(Equal(numFiles))
}

// hangingEmbedder simulates Ollama hanging indefinitely on Embed().
// This reproduces the deadlock observed in production: all semaphore slots
// get consumed by goroutines stuck in Embed(), and no progress is ever made.
type hangingEmbedder struct {
	callCount atomic.Int32
}

func (h *hangingEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	h.callCount.Add(1)
	// Block until context is cancelled — simulates Ollama hang
	<-ctx.Done()
	return nil, ctx.Err()
}
func (h *hangingEmbedder) Generate(_ context.Context, _ string, _ ...llm.GenerateOption) (string, error) {
	return "", nil
}
func (h *hangingEmbedder) GenerateStream(_ context.Context, _ string, _ ...llm.GenerateOption) (<-chan string, <-chan error) {
	return nil, nil
}
func (h *hangingEmbedder) Name() string                  { return "hanging" }
func (h *hangingEmbedder) GetEmbeddingDimension() uint64 { return 1024 }

// TestIndexItems_EmbedHang_DoesNotDeadlock reproduces the exact bug from the log:
//
//	"Indexing stall detected … 101/181 files done, no progress for 150s.
//	 Semaphore: 0/4 slots available."
//
// Root cause: Embed() hangs (Ollama unresponsive), goroutine holds the
// semaphore slot forever, all slots fill up → deadlock.
//
// The fix: IndexItems now wraps Embed() with a 30s context.WithTimeout.
// This test uses a shorter parent timeout (5s) so the test finishes quickly.
// Before the fix, this test would hang forever.
func TestIndexItems_EmbedHang_DoesNotDeadlock(t *testing.T) {
	RegisterTestingT(t)

	hanger := &hangingEmbedder{}
	mockS := &mockStore{}
	svc := NewService(hanger, mockS)

	symbols := make([]parser.Symbol, 10)
	for i := 0; i < 10; i++ {
		symbols[i] = parser.Symbol{
			Name:      fmt.Sprintf("HangSym%d", i),
			Content:   "content",
			FilePath:  "hang.go",
			StartLine: i,
		}
	}

	// Use a 15s parent context — must allow enough time for the circuit breaker
	// to attempt health checks (PingOllama with 3s timeout) before giving up.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.IndexItems(ctx, "hang-test", symbols)
	}()

	select {
	case err := <-done:
		// We expect an error (context deadline exceeded), but NOT a hang
		Expect(err).To(HaveOccurred())
		t.Logf("IndexItems returned error (expected): %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("DEADLOCK: IndexItems hung forever — Embed() timeout is not working")
	}

	// Verify that at least one Embed call was attempted
	Expect(int(hanger.callCount.Load())).To(BeNumerically(">=", 1))
}

// TestIndexFile_EmbedHang_Timeout verifies that IndexFile respects its
// 2-minute context timeout. We use a shorter parent context here for speed.
func TestIndexFile_EmbedHang_Timeout(t *testing.T) {
	RegisterTestingT(t)

	// Create a temp Go file that will parse successfully
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "hangpkg")
	Expect(os.MkdirAll(pkgDir, 0755)).To(Succeed())
	src := "package hangpkg\n\nfunc Hanging() int { return 42 }\n"
	goFile := filepath.Join(pkgDir, "hang.go")
	Expect(os.WriteFile(goFile, []byte(src), 0644)).To(Succeed())

	hanger := &hangingEmbedder{}
	mockS := &mockStore{}
	svc := NewService(hanger, mockS)
	state := NewState()

	// 3s parent timeout — IndexFile wraps with 2min internally,
	// but parent ctx fires first
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := svc.IndexFile(ctx, "timeout-test", goFile, state)
		done <- err
	}()

	select {
	case err := <-done:
		Expect(err).To(HaveOccurred())
		t.Logf("IndexFile returned error (expected): %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: IndexFile hung — timeout mechanism failed")
	}
}
