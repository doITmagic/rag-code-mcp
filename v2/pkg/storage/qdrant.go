package storage

import (
	"context"
	"fmt"

	"github.com/qdrant/go-client/qdrant"
)

type QdrantStore struct {
	client *qdrant.Client
}

func NewQdrantStore(host string, port int, useTLS bool, apiKey string) (*QdrantStore, error) {
	config := &qdrant.Config{
		Host:   host,
		Port:   port,
		UseTLS: useTLS,
	}
	if apiKey != "" {
		config.APIKey = apiKey
	}

	client, err := qdrant.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("qdrant client: %w", err)
	}

	return &QdrantStore{client: client}, nil
}

func (s *QdrantStore) CollectionExists(ctx context.Context, name string) (bool, error) {
	return s.client.CollectionExists(ctx, name)
}

func (s *QdrantStore) CreateCollection(ctx context.Context, name string, dimension int) error {
	return s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(dimension),
			Distance: qdrant.Distance_Cosine,
		}),
	})
}

func (s *QdrantStore) Upsert(ctx context.Context, collection string, points []Point) error {
	qPoints := make([]*qdrant.PointStruct, len(points))
	for i, p := range points {
		qPoints[i] = &qdrant.PointStruct{
			Id:      qdrant.NewID(p.ID),
			Vectors: qdrant.NewVectors(p.Vector...),
			Payload: s.mapToPayload(p.Payload),
		}
	}

	wait := true
	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection,
		Points:         qPoints,
		Wait:           &wait,
	})
	return err
}

func (s *QdrantStore) Search(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error) {
	limit := uint64(query.Limit)
	if limit == 0 {
		limit = 10
	}

	withPayload := true
	resp, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQuery(query.Vector...),
		Limit:          &limit,
		WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: withPayload}},
	})
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, len(resp))
	for i, r := range resp {
		results[i] = SearchResult{
			Score: r.Score,
			Point: Point{
				ID:      r.Id.GetUuid(), // Simplified for now, assuming UUID
				Payload: s.payloadToMap(r.Payload),
			},
		}
	}
	return results, nil
}

func (s *QdrantStore) mapToPayload(m map[string]interface{}) map[string]*qdrant.Value {
	res := make(map[string]*qdrant.Value)
	for k, v := range m {
		val, _ := qdrant.NewValue(fmt.Sprintf("%v", v))
		res[k] = val
	}
	return res
}

func (s *QdrantStore) payloadToMap(p map[string]*qdrant.Value) map[string]interface{} {
	res := make(map[string]interface{})
	for k, v := range p {
		// This is a simplification; a full converter would check the kind of value
		res[k] = v.GetStringValue()
	}
	return res
}
