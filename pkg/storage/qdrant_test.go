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
}

func (f *fakeQdrantClient) CollectionExists(ctx context.Context, collectionName string) (bool, error) {
	return f.collectionExists, nil
}

func (f *fakeQdrantClient) CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error {
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

func (f *fakeQdrantClient) DeleteCollection(ctx context.Context, collectionName string) error {
	return nil
}
