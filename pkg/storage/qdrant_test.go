package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/qdrant/go-client/qdrant"
)

func TestQdrantStoreSearchDocsOnlyDedup(t *testing.T) {
	markdown := []*qdrant.ScoredPoint{
		makePoint("", 0.9, map[string]string{"file": "doc.md", "chunk_id": "1", "chunk_type": "markdown"}),
	}
	text := []*qdrant.ScoredPoint{
		makePoint("", 0.95, map[string]string{"file": "doc.md", "chunk_id": "1", "chunk_type": "text"}),
		makePoint("text-id", 0.7, map[string]string{"file": "doc.md", "chunk_id": "2", "chunk_type": "text"}),
	}
	fake := &fakeQdrantClient{queryResponses: []queryResponse{{points: markdown}, {points: text}}}
	store := NewQdrantStoreWithClient(fake)

	res, err := store.SearchDocsOnly(context.Background(), "col", SearchQuery{Vector: []float32{1}, Limit: 5})
	if err != nil {
		t.Fatalf("docs search err: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected deduped results, got %d", len(res))
	}
	if res[0].Score != 0.95 {
		t.Fatalf("expected higher score retained, got %f", res[0].Score)
	}
}

func TestQdrantStoreSearchDocsOnlySecondCallError(t *testing.T) {
	fake := &fakeQdrantClient{queryResponses: []queryResponse{{points: []*qdrant.ScoredPoint{}}, {err: errors.New("boom")}}}
	store := NewQdrantStoreWithClient(fake)
	if _, err := store.SearchDocsOnly(context.Background(), "col", SearchQuery{Vector: []float32{1}}); err == nil {
		t.Fatalf("expected second query error")
	}
}

func TestQdrantStoreSearchCodeOnlyMergesFilters(t *testing.T) {
	fake := &fakeQdrantClient{queryResponses: []queryResponse{{points: []*qdrant.ScoredPoint{}}}}
	store := NewQdrantStoreWithClient(fake)
	f := map[string]interface{}{"lang": "go"}
	_, err := store.SearchCodeOnly(context.Background(), "col", SearchQuery{Vector: []float32{1}, Filter: f})
	if err != nil {
		t.Fatalf("code search err: %v", err)
	}
	if len(fake.queryCalls) != 1 {
		t.Fatalf("expected single query call")
	}
	filter := fake.queryCalls[0].Filter
	if filter == nil || len(filter.Must) == 0 {
		t.Fatalf("expected merged Must filters")
	}
}

func TestQdrantStoreSearchCodeOnlyExcludesDocs(t *testing.T) {
	fake := &fakeQdrantClient{queryResponses: []queryResponse{{points: []*qdrant.ScoredPoint{makePoint("code", 0.8, map[string]string{"chunk_type": "code"})}}}}
	store := NewQdrantStoreWithClient(fake)
	res, err := store.SearchCodeOnly(context.Background(), "col", SearchQuery{Vector: []float32{1}, Limit: 1})
	if err != nil || len(res) != 1 {
		t.Fatalf("unexpected result %v, %v", res, err)
	}
	filter := fake.queryCalls[0].Filter
	if filter == nil || len(filter.MustNot) < 2 {
		t.Fatalf("expected chunk_type MustNot filters")
	}
}

func TestQdrantStoreUpsertPayloadMapping(t *testing.T) {
	fake := &fakeQdrantClient{}
	store := NewQdrantStoreWithClient(fake)
	points := []Point{{
		ID:     "123",
		Vector: []float32{1, 2},
		Payload: map[string]interface{}{
			"file":     "main.go",
			"chunk_id": 5,
			"active":   true,
		},
	}}
	if _, err := store.Upsert(context.Background(), "col", points); err != nil {
		t.Fatalf("upsert err: %v", err)
	}
	if len(fake.upsertRequests) != 1 {
		t.Fatalf("expected single upsert request")
	}
	payload := fake.upsertRequests[0].Points[0].Payload
	if payload["file"].GetStringValue() != "main.go" {
		t.Errorf("expected file=main.go, got %v", payload["file"].GetStringValue())
	}
	if payload["chunk_id"].GetIntegerValue() != 5 {
		t.Errorf("expected chunk_id=5, got %v", payload["chunk_id"].GetIntegerValue())
	}
	if payload["active"].GetBoolValue() != true {
		t.Errorf("expected active=true, got %v", payload["active"].GetBoolValue())
	}
	if t.Failed() {
		t.Fatalf("payload mapping failed: %#v", payload)
	}
}

func TestPointIDToString(t *testing.T) {
	tests := []struct {
		name string
		id   *qdrant.PointId
		want string
	}{
		{
			name: "nil id",
			id:   nil,
			want: "",
		},
		{
			name: "string id",
			id:   qdrant.NewID("abc"),
			want: "abc",
		},
		{
			name: "numeric id",
			id:   qdrant.NewIDNum(42),
			want: "42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if s := pointIDToString(tt.id); s != tt.want {
				t.Errorf("pointIDToString() = %q, want %q", s, tt.want)
			}
		})
	}
	if s := pointIDToString(qdrant.NewIDNum(42)); s != "42" {
		t.Fatalf("expected numeric 42, got %s", s)
	}
}

func TestNormalizeLimit(t *testing.T) {
	if normalizeLimit(0) != 10 {
		t.Fatalf("expected default 10")
	}
	if normalizeLimit(5) != 5 {
		t.Fatalf("expected passthrough")
	}
}

func makePoint(id string, score float32, payload map[string]string) *qdrant.ScoredPoint {
	qPayload := make(map[string]*qdrant.Value)
	for k, v := range payload {
		qPayload[k] = qdrant.NewValueString(v)
	}
	return &qdrant.ScoredPoint{
		Id:      qdrant.NewID(id),
		Score:   score,
		Payload: qPayload,
	}
}

type scrollResponse struct {
	points []*qdrant.RetrievedPoint
	err    error
}

type queryResponse struct {
	points []*qdrant.ScoredPoint
	err    error
}

type fakeQdrantClient struct {
	collectionExists bool
	collectionInfo   *qdrant.CollectionInfo
	queryResponses   []queryResponse
	queryCalls       []*qdrant.QueryPoints
	upsertRequests   []*qdrant.UpsertPoints
	scrollRequests   []*qdrant.ScrollPoints
	scrollResponses  []scrollResponse
	createRequests   []*qdrant.CreateCollection
}

func (f *fakeQdrantClient) CollectionExists(ctx context.Context, collectionName string) (bool, error) {
	return f.collectionExists, nil
}

func (f *fakeQdrantClient) CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error {
	f.createRequests = append(f.createRequests, req)
	return nil
}

func (f *fakeQdrantClient) Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error) {
	f.upsertRequests = append(f.upsertRequests, req)
	return &qdrant.UpdateResult{}, nil
}

func (f *fakeQdrantClient) Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
	f.queryCalls = append(f.queryCalls, req)
	if len(f.queryResponses) == 0 {
		return nil, nil
	}
	resp := f.queryResponses[0]
	f.queryResponses = f.queryResponses[1:]
	return resp.points, resp.err
}

func (f *fakeQdrantClient) GetCollectionInfo(ctx context.Context, collectionName string) (*qdrant.CollectionInfo, error) {
	return f.collectionInfo, nil
}

func (f *fakeQdrantClient) Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error) {
	return &qdrant.UpdateResult{}, nil
}

func (f *fakeQdrantClient) Scroll(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error) {
	f.scrollRequests = append(f.scrollRequests, req)
	if len(f.scrollResponses) == 0 {
		return nil, nil
	}
	resp := f.scrollResponses[0]
	f.scrollResponses = f.scrollResponses[1:]
	return resp.points, resp.err
}

func (f *fakeQdrantClient) DeleteCollection(ctx context.Context, collectionName string) error {
	return nil
}

// ─── ExactSearch tests ─────────────────────────────────────────────────────────

// TestExactSearchUsesScrollNotQuery verifies that ExactSearch uses Scroll (no
// HNSW, no embedding), NOT the Query endpoint.
func TestExactSearchUsesScrollNotQuery(t *testing.T) {
	retrievedPoint := &qdrant.RetrievedPoint{
		Id:      qdrant.NewID("abc"),
		Payload: map[string]*qdrant.Value{"name": qdrant.NewValueString("Foo")},
	}
	fake := &fakeQdrantClient{
		scrollResponses: []scrollResponse{{points: []*qdrant.RetrievedPoint{retrievedPoint}}},
	}
	store := NewQdrantStoreWithClient(fake)

	results, err := store.ExactSearch(context.Background(), "col", map[string]interface{}{"name": "Foo"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must NOT have called Query (HNSW / embedding path)
	if len(fake.queryCalls) != 0 {
		t.Fatalf("ExactSearch must NOT call Query, called %d time(s)", len(fake.queryCalls))
	}

	// Must have called Scroll exactly once
	if len(fake.scrollRequests) != 1 {
		t.Fatalf("ExactSearch must call Scroll once, called %d time(s)", len(fake.scrollRequests))
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Score != 1.0 {
		t.Fatalf("exact search score must be 1.0, got %f", results[0].Score)
	}
	if results[0].Point.ID != "abc" {
		t.Fatalf("expected ID abc, got %s", results[0].Point.ID)
	}
	if results[0].Point.Payload["name"] != "Foo" {
		t.Fatalf("expected payload name=Foo, got %v", results[0].Point.Payload["name"])
	}
}

// TestExactSearchNestedArrayFilter verifies that "relations[].target_name"
// produces a Qdrant NestedCondition (not a plain FieldCondition).
func TestExactSearchNestedArrayFilter(t *testing.T) {
	fake := &fakeQdrantClient{
		scrollResponses: []scrollResponse{{points: nil}},
	}
	store := NewQdrantStoreWithClient(fake)

	_, err := store.ExactSearch(context.Background(), "col", map[string]interface{}{
		"relations[].target_name": "IndexItems",
	}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.scrollRequests) != 1 {
		t.Fatalf("expected 1 scroll call, got %d", len(fake.scrollRequests))
	}
	req := fake.scrollRequests[0]
	if req.Filter == nil || len(req.Filter.Must) != 1 {
		t.Fatalf("expected 1 Must condition in filter")
	}

	cond := req.Filter.Must[0]
	nested := cond.GetNested()
	if nested == nil {
		t.Fatalf("expected Nested condition for relations[].target_name, got: %T", cond.ConditionOneOf)
	}
	if nested.Key != "relations" {
		t.Fatalf("expected nested key 'relations', got %q", nested.Key)
	}
	innerFilter := nested.Filter
	if innerFilter == nil || len(innerFilter.Must) != 1 {
		t.Fatalf("expected inner Must condition in nested filter")
	}
	innerField := innerFilter.Must[0].GetField()
	if innerField == nil || innerField.Key != "target_name" {
		t.Fatalf("expected inner field key 'target_name', got %v", innerField)
	}
	if innerField.GetMatch().GetKeyword() != "IndexItems" {
		t.Fatalf("expected keyword 'IndexItems', got %q", innerField.GetMatch().GetKeyword())
	}
}

// TestCreateCollectionSetsHNSWAndOptimizers verifies that CreateCollection
// forwards HNSW and optimizer configuration to Qdrant.
func TestCreateCollectionSetsHNSWAndOptimizers(t *testing.T) {
	fake := &fakeQdrantClient{}
	store := NewQdrantStoreWithClient(fake)

	if err := store.CreateCollection(context.Background(), "test-col", 1024); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.createRequests) != 1 {
		t.Fatalf("expected 1 create request, got %d", len(fake.createRequests))
	}
	req := fake.createRequests[0]

	if req.HnswConfig == nil {
		t.Fatal("expected HnswConfig to be set")
	}
	if req.HnswConfig.GetM() != 16 {
		t.Fatalf("expected HNSW M=16, got %d", req.HnswConfig.GetM())
	}
	if req.HnswConfig.GetEfConstruct() != 100 {
		t.Fatalf("expected EfConstruct=100, got %d", req.HnswConfig.GetEfConstruct())
	}
	if req.HnswConfig.GetFullScanThreshold() != 1000 {
		t.Fatalf("expected FullScanThreshold=1000, got %d", req.HnswConfig.GetFullScanThreshold())
	}

	if req.OptimizersConfig == nil {
		t.Fatal("expected OptimizersConfig to be set")
	}
	if req.OptimizersConfig.GetDefaultSegmentNumber() != 2 {
		t.Fatalf("expected DefaultSegmentNumber=2, got %d", req.OptimizersConfig.GetDefaultSegmentNumber())
	}
	if req.OptimizersConfig.GetMemmapThreshold() != 10000 {
		t.Fatalf("expected MemmapThreshold=10000, got %d", req.OptimizersConfig.GetMemmapThreshold())
	}
}
