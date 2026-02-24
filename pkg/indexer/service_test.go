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
	upsertPoints []storage.Point
	mu           sync.Mutex
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
	return nil
}

func (m *mockStore) DeleteCollection(ctx context.Context, collection string) error {
	return nil
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

// TestIndexWorkspaceParallelFiles verifies that multiple files are indexed concurrently
// with no data races on shared state. Run with -race to detect races.
func TestIndexWorkspaceParallelFiles(t *testing.T) {
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

	err := svc.IndexWorkspace(context.Background(), dir, "test-parallel", Options{Language: "go"})
	Expect(err).NotTo(HaveOccurred())

	// Each sub-package has 1 function → numFiles symbols total
	mockS.mu.Lock()
	total := len(mockS.upsertPoints)
	mockS.mu.Unlock()

	Expect(total).To(Equal(numFiles))
	Expect(int(atomic.LoadInt32(&mockEmbed.embedCount))).To(BeNumerically(">=", numFiles))
}
