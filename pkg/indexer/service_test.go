package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/docs" // register JSON analyzer
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"   // register Go analyzer
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	. "github.com/onsi/gomega"
)

type mockEmbedder struct {
	embedCount int32
}

type mockBatchEmbedder struct {
	mockEmbedder
	batchCount int32
}

func (m *mockBatchEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	atomic.AddInt32(&m.batchCount, 1)
	vectors := make([][]float64, len(texts))
	for i := range texts {
		vectors[i] = make([]float64, 1024)
	}
	return vectors, nil
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
	upsertPoints      []storage.Point
	deletedPrefixes   []string
	deletedFilters    []string
	collectionMissing bool
	vectorSize        uint64 // reported by GetCollectionInfo; 0 means unknown
	mu                sync.Mutex
}

func (m *mockStore) GetCollectionInfo(ctx context.Context, collection string) (*storage.CollectionInfo, error) {
	return &storage.CollectionInfo{VectorSize: m.vectorSize}, nil
}

func (m *mockStore) Upsert(ctx context.Context, collection string, points []storage.Point) (*storage.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertPoints = append(m.upsertPoints, points...)
	return nil, nil
}

func (m *mockStore) CollectionExists(ctx context.Context, collection string) (bool, error) {
	return !m.collectionMissing, nil
}

func (m *mockStore) CreateCollection(ctx context.Context, collection string, dimension int) error {
	m.collectionMissing = false
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

	cleaned, err := svc.cleanupStaleFiles(context.Background(), wsRoot, "test_collection", state, "go")
	if err != nil {
		t.Fatal(err)
	}
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

func TestIndexItemsUsesBoundedEmbeddingBatches(t *testing.T) {
	embedder := &mockBatchEmbedder{}
	store := &mockStore{}
	service := NewService(embedder, store)
	symbols := make([]parser.Symbol, 5)
	for i := range symbols {
		symbols[i] = parser.Symbol{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("Symbol%d", i)}
	}

	if err := service.IndexItems(context.Background(), "batch-test", symbols); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&embedder.batchCount); got != 3 {
		t.Fatalf("batch calls = %d, want 3", got)
	}
	if len(store.upsertPoints) != len(symbols) {
		t.Fatalf("upserted points = %d, want %d", len(store.upsertPoints), len(symbols))
	}
}

func TestSymbolsForFileDropsPackageSiblings(t *testing.T) {
	target := filepath.Join("workspace", "handler.go")
	symbols := []parser.Symbol{
		{Name: "Handler", FilePath: target},
		{Name: "Sibling", FilePath: filepath.Join("workspace", "service.go")},
		{Name: "Unknown"},
	}

	got := symbolsForFile(symbols, target)
	if len(got) != 1 || got[0].Name != "Handler" {
		t.Fatalf("symbols = %#v, want only Handler", got)
	}
}

func TestIndexStateIsolatedByCollection(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".ragcode", "state.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"files":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package sample\nfunc ComputeInvoiceTotal() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	for i, collection := range []string{"main-go", "feature-go", "main-go"} {
		before := len(store.upsertPoints)
		if err := svc.IndexWorkspace(context.Background(), root, collection, Options{Language: "go"}); err != nil {
			t.Fatal(err)
		}
		added := len(store.upsertPoints) - before
		if i < 2 && added == 0 || i == 2 && added != 0 {
			t.Fatalf("collection %s: indexed %d", collection, added)
		}
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy state was not removed: %v", err)
	}
	// A restored state file must not suppress rebuilding a deleted collection.
	store.collectionMissing = true
	before := len(store.upsertPoints)
	if err := svc.IndexWorkspace(context.Background(), root, "main-go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if len(store.upsertPoints) == before || store.collectionMissing {
		t.Fatal("missing collection not rebuilt from unchanged files")
	}
}

func TestCountAllFilesSkipsLowValueFiles(t *testing.T) {
	root := t.TempDir()
	createFile(t, filepath.Join(root, "firebase-service-account.json"))
	createFile(t, filepath.Join(root, "package-lock.json"))
	createFile(t, filepath.Join(root, "safe.json"))

	result := (&Service{}).CountAllFiles(root, nil)
	if result.Counts["docs"] != 2 {
		t.Fatalf("counts = %v, want both non-lock JSON files", result.Counts)
	}

	for path, want := range map[string]bool{
		".env":                          false,
		"firebase-service-account.json": false,
		".npmrc":                        true,
		"id_ed25519":                    true,
		"certificate.pem":               true,
		"package-lock.json":             true,
		"go.sum":                        true,
	} {
		// Outside a Git repository: a .env is skipped only when Git ignores it.
		if got := ShouldSkipFile(filepath.Join(root, path)); got != want {
			t.Errorf("ShouldSkipFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIndexWorkspaceDeletesExcludedFileOnlyWhenTracked(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "package-lock.json")
	createFile(t, path)
	store := &mockStore{}
	service := NewService(&mockEmbedder{}, store)

	if err := service.IndexWorkspace(context.Background(), root, "docs", Options{}); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 0 {
		t.Fatalf("untracked exclusion caused deletes: %v", store.deletedFilters)
	}

	state := NewState()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	state.UpdateFile(path, info)
	if err := state.Save(StatePath(root, "docs")); err != nil {
		t.Fatal(err)
	}
	if err := service.IndexWorkspace(context.Background(), root, "docs", Options{}); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 1 || store.deletedFilters[0] != path {
		t.Fatalf("tracked exclusion deletes = %v, want [%s]", store.deletedFilters, path)
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

// A file removed from disk must lose its vectors and its state entry; the
// watcher path cannot Analyze it, so this is the only way it is cleaned up.
func TestRemoveFile(t *testing.T) {
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	state := NewState()
	path := filepath.Join(t.TempDir(), "gone.go")
	state.Files[path] = FileState{}

	if err := svc.RemoveFile(context.Background(), "col", path, state); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 1 || store.deletedFilters[0] != path {
		t.Fatalf("deleted filters = %v, want [%s]", store.deletedFilters, path)
	}
	if _, ok := state.Files[path]; ok {
		t.Fatal("state still tracks the removed file")
	}
}

type concurrentMockEmbedder struct {
	mockEmbedder
	active atomic.Int32
	max    atomic.Int32
}

func (m *concurrentMockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	cur := m.active.Add(1)
	for {
		prev := m.max.Load()
		if cur <= prev || m.max.CompareAndSwap(prev, cur) {
			break
		}
	}
	time.Sleep(25 * time.Millisecond)
	m.active.Add(-1)
	return make([]float64, 1024), nil
}

func TestSetEmbedBatchSizeAndWorkers(t *testing.T) {
	embedder := &mockBatchEmbedder{}
	store := &mockStore{}
	service := NewService(embedder, store)
	service.SetEmbedBatchSize(4)
	service.SetEmbedBatchSize(0) // ignored

	symbols := make([]parser.Symbol, 8)
	for i := range symbols {
		symbols[i] = parser.Symbol{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("Symbol%d", i)}
	}

	if err := service.IndexItems(context.Background(), "batch-size-4", symbols); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&embedder.batchCount); got != 2 {
		t.Fatalf("batch calls = %d, want 2 (8 symbols with batch size 4)", got)
	}

	concEmbedder := &concurrentMockEmbedder{}
	concService := NewService(concEmbedder, &mockStore{})
	concService.SetEmbedWorkers(3)
	concService.SetEmbedWorkers(-1) // ignored

	if err := concService.IndexItems(context.Background(), "workers-3", symbols); err != nil {
		t.Fatal(err)
	}
	if got := concEmbedder.max.Load(); got < 2 {
		t.Fatalf("max concurrent workers = %d, want >= 2 with SetEmbedWorkers(3)", got)
	}
}

type failingEmbedder struct {
	mockEmbedder
	fail atomic.Bool
}

func (m *failingEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if m.fail.Load() {
		return nil, errors.New("embedding service unavailable")
	}
	return m.mockEmbedder.Embed(ctx, text)
}

// A run where a file fails to embed (Ollama down mid-run) must not report
// success, and the next run must retry the file.
func TestIndexWorkspaceReportsAndRetriesFailedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Work() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	embedder := &failingEmbedder{}
	embedder.fail.Store(true)
	store := &mockStore{}
	svc := NewService(embedder, store)

	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err == nil {
		t.Fatal("indexing reported success although the only file failed to embed")
	}
	embedder.fail.Store(false)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if len(store.upsertPoints) != 1 {
		t.Fatalf("upserted %d points after recovery, want 1 (failed file not retried)", len(store.upsertPoints))
	}
}

type editingEmbedder struct {
	mockEmbedder
	edit func()
}

func (m *editingEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if edit := m.edit; edit != nil {
		m.edit = nil
		edit()
	}
	return m.mockEmbedder.Embed(ctx, text)
}

// An edit saved while the old content is being embedded must be picked up by
// the next run, not hidden behind state recording the post-edit metadata.
func TestIndexWorkspacePicksUpEditMadeDuringEmbedding(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc Old() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	embedder := &editingEmbedder{edit: func() {
		if err := os.WriteFile(path, []byte("package main\nfunc NewAndLonger() {}\n"), 0o644); err != nil {
			t.Error(err)
		}
	}}
	store := &mockStore{}
	svc := NewService(embedder, store)

	for i := 0; i < 2; i++ {
		if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range store.upsertPoints {
		if p.Payload["name"] == "NewAndLonger" {
			return
		}
	}
	t.Fatalf("edited content never indexed; upserted %d point(s)", len(store.upsertPoints))
}

// A file deleted after the scan listed it is gone, not a failure of the run.
func TestIndexWorkspaceSkipsFileDeletedDuringRun(t *testing.T) {
	root := t.TempDir()
	for name, symbol := range map[string]string{"a.go": "First", "z.go": "Second"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\nfunc "+symbol+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deleted := filepath.Join(root, "z.go")
	embedder := &editingEmbedder{edit: func() {
		if err := os.Remove(deleted); err != nil {
			t.Error(err)
		}
	}}
	svc := NewService(embedder, &mockStore{})
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatalf("deleting a file during the run failed it: %v", err)
	}
	state, err := LoadState(StatePath(root, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, tracked := state.GetFileState(deleted); tracked {
		t.Fatal("deleted file kept in state")
	}
}

type deleteFailStore struct {
	mockStore
	fail bool
}

func (m *deleteFailStore) DeleteByFilter(ctx context.Context, collection, field string, value interface{}) error {
	if m.fail {
		return errors.New("vector store unavailable")
	}
	return m.mockStore.DeleteByFilter(ctx, collection, field, value)
}

func (m *deleteFailStore) DeleteByPrefix(ctx context.Context, collection, key, prefix string) (int, error) {
	if m.fail {
		return 0, errors.New("vector store unavailable")
	}
	return m.mockStore.DeleteByPrefix(ctx, collection, key, prefix)
}

// A deleted file whose vectors could not be removed (vector store briefly
// down) must stay in state, so the next run deletes them instead of leaving
// them searchable forever.
func TestStaleCleanupRetriesFailedDeletes(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "gone.go")
	dir := filepath.Join(root, "removed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(dir, "one.go")} {
		if err := os.WriteFile(path, []byte("package main\nfunc Work() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := &deleteFailStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	store.fail = true
	_ = svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"})
	store.fail = false
	store.deletedFilters, store.deletedPrefixes = nil, nil
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 1 || store.deletedFilters[0] != file {
		t.Fatalf("file deletes on retry = %v, want [%s]", store.deletedFilters, file)
	}
	if want := dir + string(os.PathSeparator); len(store.deletedPrefixes) != 1 || store.deletedPrefixes[0] != want {
		t.Fatalf("directory deletes on retry = %v, want [%s]", store.deletedPrefixes, want)
	}
}

type dropTrackingStore struct {
	mockStore
	dropped bool
}

func (m *dropTrackingStore) DeleteCollection(context.Context, string) error {
	m.dropped = true
	m.collectionMissing = true
	return nil
}

// The engine recreates every language collection. One whose files are all
// gone must still be dropped, or its old vectors stay searchable.
func TestRecreateDropsCollectionOfLanguageWithoutFiles(t *testing.T) {
	root := t.TempDir()
	store := &dropTrackingStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go", Recreate: true}); err != nil {
		t.Fatal(err)
	}
	if !store.dropped {
		t.Fatal("recreate kept the collection of a language with no files left")
	}
}

// Switching ollama_embed to a model with another dimension left the old
// collection in place: every query failed with a vector dimension error and
// each edited file lost its points when the upsert was rejected.
func TestIndexWorkspaceRebuildsCollectionOfAnotherDimension(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &dropTrackingStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	// Built with a 768-dim model; the embedder now returns 1024 dims.
	store.vectorSize = 768
	store.upsertPoints = nil
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if !store.dropped || len(store.upsertPoints) != 1 {
		t.Fatalf("dropped=%v and indexed %d file(s), want the collection rebuilt with 1", store.dropped, len(store.upsertPoints))
	}
}

// A directory added to exclude_patterns after it was indexed is pruned by the
// scanner, so its files never reach the per-file exclusion check; their
// vectors must still be removed.
func TestIndexWorkspaceDropsNewlyExcludedDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "secret.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package private\nfunc Secret() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	store.deletedFilters = nil
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go", ExcludePatterns: []string{"private"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 1 || store.deletedFilters[0] != path {
		t.Fatalf("deletes = %v, want [%s]", store.deletedFilters, path)
	}
	state, err := LoadState(StatePath(root, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, tracked := state.GetFileState(path); tracked {
		t.Fatal("state still tracks the excluded file")
	}
}

// A directory the scanner cannot list (EACCES, EIO on a network mount) is
// skipped silently; its files still exist and must keep their vectors.
func TestIndexWorkspaceKeepsFilesOfUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	path := filepath.Join(dir, "a.go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package pkg\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	store.deletedFilters = nil
	// Searchable but not listable: the file stats fine, the walk cannot read the directory.
	if err := os.Chmod(dir, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 0 {
		t.Fatalf("deleted vectors of files that still exist: %v", store.deletedFilters)
	}
	state, err := LoadState(StatePath(root, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, tracked := state.GetFileState(path); !tracked {
		t.Fatal("state dropped a file that still exists")
	}
}

// slowBatchEmbedder answers one text at a time but cannot finish a batch
// within the embed budget, like a CPU-only Ollama on large symbols.
type slowBatchEmbedder struct {
	mockEmbedder
}

func (m *slowBatchEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return [][]float64{make([]float64, 1024)}, nil
}

// TestIndexItems_SlowBatchFallsBackToSingleEmbeds: a batch that runs out of
// time must not take every symbol in it out of the index.
func TestIndexItems_SlowBatchFallsBackToSingleEmbeds(t *testing.T) {
	RegisterTestingT(t)

	mockS := &mockStore{}
	svc := NewService(&slowBatchEmbedder{}, mockS)
	symbols := []parser.Symbol{
		{Name: "Big1", Content: "content", FilePath: "big.ts", StartLine: 1},
		{Name: "Big2", Content: "content", FilePath: "big.ts", StartLine: 2},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	Expect(svc.IndexItems(ctx, "slow-batch", symbols)).To(Succeed())
	Expect(mockS.upsertPoints).To(HaveLen(2))
}

// oneBadSymbolEmbedder fails every batch and, alone, only the symbol named Bad.
type oneBadSymbolEmbedder struct {
	mockEmbedder
}

func (m *oneBadSymbolEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 1 || strings.Contains(texts[0], "\nBad\n") {
		return nil, errors.New("embed failed")
	}
	return [][]float64{make([]float64, 1024)}, nil
}

// TestIndexItems_SingleEmbedFailureKeepsRestOfBatch: one symbol that cannot be
// embedded on its own must not keep the rest of its batch out of the index.
func TestIndexItems_SingleEmbedFailureKeepsRestOfBatch(t *testing.T) {
	RegisterTestingT(t)

	mockS := &mockStore{}
	svc := NewService(&oneBadSymbolEmbedder{}, mockS)
	svc.SetEmbedBatchSize(3)
	symbols := []parser.Symbol{
		{Name: "Good1", Content: "content", FilePath: "a.ts", StartLine: 1},
		{Name: "Bad", Content: "content", FilePath: "a.ts", StartLine: 2},
		{Name: "Good2", Content: "content", FilePath: "a.ts", StartLine: 3},
	}

	Expect(svc.IndexItems(context.Background(), "one-bad", symbols)).To(MatchError(ContainSubstring("Bad")))
	Expect(mockS.upsertPoints).To(HaveLen(2))
}

// activitySpyEmbedder fails every batch and records, at each single embed,
// the stall watchdog's last activity time.
type activitySpyEmbedder struct {
	mockEmbedder
	svc      *Service
	activity []int64
}

func (m *activitySpyEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 1 {
		return nil, errors.New("embed failed")
	}
	m.activity = append(m.activity, m.svc.lastActivity.Load())
	return [][]float64{make([]float64, 1024)}, nil
}

// TestIndexItems_SingleEmbedsKeepWatchdogAlive: the single embeds of a failed
// batch can take minutes; each one that succeeds is progress, or the stall
// watchdog restarts Ollama in the middle of a working embed.
func TestIndexItems_SingleEmbedsKeepWatchdogAlive(t *testing.T) {
	RegisterTestingT(t)

	embedder := &activitySpyEmbedder{}
	svc := NewService(embedder, &mockStore{})
	embedder.svc = svc
	svc.SetEmbedBatchSize(3)
	symbols := []parser.Symbol{
		{Name: "A", Content: "content", FilePath: "a.ts", StartLine: 1},
		{Name: "B", Content: "content", FilePath: "a.ts", StartLine: 2},
		{Name: "C", Content: "content", FilePath: "a.ts", StartLine: 3},
	}

	svc.lastActivity.Store(0)
	Expect(svc.IndexItems(context.Background(), "activity", symbols)).To(Succeed())
	Expect(embedder.activity).To(HaveLen(3))
	Expect(embedder.activity[1]).NotTo(BeZero(), "no activity recorded after the first single embed")
}

// A process killed after a recreate dropped the collection, and before the
// first state save, must not leave the old state on disk claiming files whose
// vectors are gone: the next run would see no changes and never re-embed them.
func TestRecreateCrashBeforeStateSaveReindexesOnNextRun(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, name+".go"), []byte("package main\nfunc Work"+name+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := &dropTrackingStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}

	// Crash (no deferred save, like kill -9) right after the first file.
	func() {
		defer func() { _ = recover() }()
		_ = svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go", Recreate: true,
			Progress: func(int, int) { panic("killed") }})
	}()
	if !store.dropped {
		t.Fatal("recreate did not drop the collection")
	}

	store.upsertPoints = nil
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if len(store.upsertPoints) != 2 {
		t.Fatalf("re-indexed %d file(s) after the crash, want 2", len(store.upsertPoints))
	}
}

// A .ragcode the daemon cannot write (left by a run as another user, say)
// only costs the saved state; a recreate must still rebuild the collection.
func TestRecreateWithReadOnlyStateDirStillIndexes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &dropTrackingStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".ragcode")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("running with permissions that ignore a read-only directory")
	}
	store.upsertPoints = nil
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go", Recreate: true}); err != nil {
		t.Fatal(err)
	}
	if !store.dropped || len(store.upsertPoints) != 1 {
		t.Fatalf("recreate dropped=%v and indexed %d file(s), want 1", store.dropped, len(store.upsertPoints))
	}
}

// A state file holding {"files": null} must be indexed over, not crash the
// daemon with a nil map write on every run.
func TestIndexWorkspaceSurvivesNullFilesState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package sample\nfunc ComputeInvoiceTotal() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	statePath := StatePath(root, "main-go")
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"files": null}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "main-go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	if len(store.upsertPoints) == 0 {
		t.Fatal("nothing indexed")
	}
}
