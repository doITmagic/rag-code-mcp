package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/qdrant/go-client/qdrant"
)

type qdrantClient interface {
	CollectionExists(ctx context.Context, collectionName string) (bool, error)
	CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error
	Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
	GetCollectionInfo(ctx context.Context, collectionName string) (*qdrant.CollectionInfo, error)
	Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error)
	Scroll(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error)
	DeleteCollection(ctx context.Context, collectionName string) error
}

type QdrantStore struct {
	client qdrantClient
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

// NewQdrantStoreWithClient is exposed for tests to inject a fake client.
func NewQdrantStoreWithClient(client qdrantClient) *QdrantStore {
	return &QdrantStore{client: client}
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
		// HNSW tuning for balanced accuracy/speed on local machines.
		HnswConfig: &qdrant.HnswConfigDiff{
			M:                 ptr(uint64(16)),
			EfConstruct:       ptr(uint64(100)),
			FullScanThreshold: ptr(uint64(1000)),
		},
		// Fewer segments for lower background CPU usage on developer machines.
		OptimizersConfig: &qdrant.OptimizersConfigDiff{
			DefaultSegmentNumber: ptr(uint64(2)),
			MemmapThreshold:      ptr(uint64(10000)),
		},
	})
}

func (s *QdrantStore) DeleteCollection(ctx context.Context, name string) error {
	return s.client.DeleteCollection(ctx, name)
}

func (s *QdrantStore) Upsert(ctx context.Context, collection string, points []Point) (*UpdateResult, error) {
	qPoints := make([]*qdrant.PointStruct, len(points))
	for i, p := range points {
		qPoints[i] = &qdrant.PointStruct{
			Id:      qdrant.NewID(p.ID),
			Vectors: qdrant.NewVectors(p.Vector...),
			Payload: s.mapToPayload(p.Payload),
		}
	}

	wait := true
	return s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection,
		Points:         qPoints,
		Wait:           &wait,
	})
}

func (s *QdrantStore) Search(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error) {
	limit := normalizeLimit(query.Limit)
	withPayload := true
	resp, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQuery(query.Vector...),
		Limit:          &limit,
		WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: withPayload}},
		Filter:         buildFilter(query.Filter),
	})
	if err != nil {
		return nil, err
	}

	return s.toSearchResults(resp), nil
}

func (s *QdrantStore) SearchDocsOnly(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error) {
	markdown, err := s.searchByChunkType(ctx, collection, query, "markdown")
	if err != nil {
		return nil, fmt.Errorf("search docs markdown: %w", err)
	}
	text, err := s.searchByChunkType(ctx, collection, query, "text")
	if err != nil {
		return nil, fmt.Errorf("search docs text: %w", err)
	}

	merged := make(map[string]SearchResult)
	for _, result := range append(markdown, text...) {
		key := result.Point.ID
		if key == "" {
			file := fmt.Sprintf("%v", result.Point.Payload["file"])
			chunkID := fmt.Sprintf("%v", result.Point.Payload["chunk_id"])
			key = file + "#" + chunkID
		}
		if existing, ok := merged[key]; !ok || result.Score > existing.Score {
			merged[key] = result
		}
	}

	results := make([]SearchResult, 0, len(merged))
	for _, r := range merged {
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > int(query.Limit) && query.Limit > 0 {
		results = results[:query.Limit]
	}
	return results, nil
}

func (s *QdrantStore) SearchCodeOnly(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error) {
	limit := normalizeLimit(query.Limit)
	withPayload := true
	filter := &qdrant.Filter{
		MustNot: []*qdrant.Condition{
			matchKeyword("chunk_type", "markdown"),
			matchKeyword("chunk_type", "text"),
		},
	}
	baseFilter := buildFilter(query.Filter)
	if baseFilter != nil {
		filter.Must = append(filter.Must, baseFilter.Must...)
		filter.Should = append(filter.Should, baseFilter.Should...)
		filter.MustNot = append(filter.MustNot, baseFilter.MustNot...)
	}

	resp, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQuery(query.Vector...),
		Limit:          &limit,
		WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: withPayload}},
		Filter:         filter,
	})
	if err != nil {
		return nil, fmt.Errorf("search code: %w", err)
	}
	return s.toSearchResults(resp), nil
}

func (s *QdrantStore) SearchByChunkType(ctx context.Context, collection string, query SearchQuery, chunkType string) ([]SearchResult, error) {
	return s.searchByChunkType(ctx, collection, query, chunkType)
}

// ExactSearch performs a metadata-only filter scan via Qdrant Scroll.
// Unlike Search/SearchCodeOnly, this never calls the HNSW index and requires no embeddings.
// Results are assigned score=1.0 (all matches are equally exact).
// Supports nested array filter paths using "array[].field" notation.
func (s *QdrantStore) ExactSearch(ctx context.Context, collection string, filters map[string]interface{}, limit int) ([]SearchResult, error) {
	qFilters := make([]*qdrant.Condition, 0, len(filters))
	for k, v := range filters {
		if b, ok := v.(bool); ok {
			qFilters = append(qFilters, matchBool(k, b))
		} else {
			qFilters = append(qFilters, toCondition(k, fmt.Sprintf("%v", v)))
		}
	}

	lim := uint32(10)
	if limit > 0 {
		lim = uint32(limit)
	}

	withPayload := true
	res, err := s.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: collection,
		Filter:         &qdrant.Filter{Must: qFilters},
		Limit:          ptr(lim),
		WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: withPayload}},
	})
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(res))
	for _, p := range res {
		results = append(results, SearchResult{
			Point: Point{
				ID:      pointIDToString(p.Id),
				Payload: s.payloadToMap(p.Payload),
			},
			Score: 1.0,
		})
	}
	return results, nil
}

// toCondition converts a filter key-value pair into a Qdrant condition.
// Supports nested array paths using the "array[].field" notation,
// e.g. "relations[].target_name" → NestedCondition on "relations" filtering by "target_name".
func toCondition(key, value string) *qdrant.Condition {
	const nestedSep = "[]."
	if idx := strings.Index(key, nestedSep); idx >= 0 {
		arrayField := key[:idx]
		nestedField := key[idx+len(nestedSep):]
		return qdrant.NewNestedFilter(arrayField, &qdrant.Filter{
			Must: []*qdrant.Condition{
				matchKeyword(nestedField, value),
			},
		})
	}
	return matchKeyword(key, value)
}

func (s *QdrantStore) mapToPayload(m map[string]interface{}) map[string]*qdrant.Value {
	res := make(map[string]*qdrant.Value, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		res[k] = s.interfaceToValue(v)
	}
	return res
}

func (s *QdrantStore) interfaceToValue(v interface{}) *qdrant.Value {
	if v == nil {
		return nil
	}
	switch typed := v.(type) {
	case string:
		return qdrant.NewValueString(typed)
	case int:
		return qdrant.NewValueInt(int64(typed))
	case int64:
		return qdrant.NewValueInt(typed)
	case float32:
		return qdrant.NewValueDouble(float64(typed))
	case float64:
		return qdrant.NewValueDouble(typed)
	case bool:
		return qdrant.NewValueBool(typed)
	case map[string]interface{}:
		fields := make(map[string]*qdrant.Value)
		for k, val := range typed {
			fields[k] = s.interfaceToValue(val)
		}
		return qdrant.NewValueStruct(&qdrant.Struct{Fields: fields})
	case []interface{}:
		values := make([]*qdrant.Value, len(typed))
		for i, val := range typed {
			values[i] = s.interfaceToValue(val)
		}
		return qdrant.NewValueList(&qdrant.ListValue{Values: values})
	default:
		// Fallback for slices of structs or other complex types
		b, err := json.Marshal(v)
		if err == nil {
			var nested any
			if err := json.Unmarshal(b, &nested); err == nil {
				return s.interfaceToValue(nested)
			}
			return qdrant.NewValueString(string(b))
		}
		return qdrant.NewValueString(fmt.Sprintf("%v", v))
	}
}

func (s *QdrantStore) payloadToMap(p map[string]*qdrant.Value) map[string]interface{} {
	res := make(map[string]interface{}, len(p))
	for k, v := range p {
		res[k] = s.valueToInterface(v)
	}
	return res
}

func (s *QdrantStore) valueToInterface(v *qdrant.Value) interface{} {
	if v == nil || v.Kind == nil {
		return nil
	}
	switch kind := v.Kind.(type) {
	case *qdrant.Value_StringValue:
		str := v.GetStringValue()
		if (strings.HasPrefix(str, "[") && strings.HasSuffix(str, "]")) || (strings.HasPrefix(str, "{") && strings.HasSuffix(str, "}")) {
			var parsed any
			if err := json.Unmarshal([]byte(str), &parsed); err == nil {
				return parsed
			}
		}
		return str
	case *qdrant.Value_IntegerValue:
		return v.GetIntegerValue()
	case *qdrant.Value_DoubleValue:
		return v.GetDoubleValue()
	case *qdrant.Value_BoolValue:
		return v.GetBoolValue()
	case *qdrant.Value_StructValue:
		fields := kind.StructValue.Fields
		res := make(map[string]interface{}, len(fields))
		for k, val := range fields {
			res[k] = s.valueToInterface(val)
		}
		return res
	case *qdrant.Value_ListValue:
		values := kind.ListValue.Values
		res := make([]interface{}, len(values))
		for i, val := range values {
			res[i] = s.valueToInterface(val)
		}
		return res
	default:
		return v.GetStringValue()
	}
}

func (s *QdrantStore) GetCollectionInfo(ctx context.Context, name string) (*CollectionInfo, error) {
	info, err := s.client.GetCollectionInfo(ctx, name)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("collection info is nil")
	}

	var vectorSize uint64
	if info.Config != nil && info.Config.Params != nil && info.Config.Params.VectorsConfig != nil {
		if params := info.Config.Params.VectorsConfig.GetParams(); params != nil {
			vectorSize = params.Size
		} else if paramsMap := info.Config.Params.VectorsConfig.GetParamsMap(); paramsMap != nil {
			for _, entry := range paramsMap.Map {
				if entry != nil {
					vectorSize = entry.Size
					break
				}
			}
		}
	}

	return &CollectionInfo{
		PointsCount: info.GetPointsCount(),
		VectorSize:  vectorSize,
	}, nil
}

func (s *QdrantStore) GetCollectionPointCount(ctx context.Context, name string) (uint64, error) {
	info, err := s.client.GetCollectionInfo(ctx, name)
	if err != nil {
		return 0, err
	}
	if info == nil {
		return 0, nil
	}
	return info.GetPointsCount(), nil
}

func (s *QdrantStore) DeleteByFilter(ctx context.Context, collection string, key string, value interface{}) error {
	wait := true
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: &qdrant.Filter{
					Must: []*qdrant.Condition{
						matchKeyword(key, fmt.Sprintf("%v", value)),
					},
				},
			},
		},
		Wait: &wait,
	})
	return err
}

// DeleteByPrefix deletes all points where payload field `key` starts with `prefix`.
func (s *QdrantStore) DeleteByPrefix(ctx context.Context, collection string, key string, prefix string) (int, error) {
	var allMatchingIDs []*qdrant.PointId
	var offset *qdrant.PointId

	payloadSelector := &qdrant.WithPayloadSelector{
		SelectorOptions: &qdrant.WithPayloadSelector_Include{
			Include: &qdrant.PayloadIncludeSelector{
				Fields: []string{key},
			},
		},
	}

	for {
		res, err := s.client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: collection,
			Limit:          ptr(uint32(10000)),
			Offset:         offset,
			WithPayload:    payloadSelector,
		})
		if err != nil {
			return 0, fmt.Errorf("scroll for prefix %q failed: %w", prefix, err)
		}

		if len(res) == 0 {
			break
		}

		for _, p := range res {
			if p.Payload != nil {
				if val, ok := p.Payload[key]; ok && val != nil {
					if strings.HasPrefix(val.GetStringValue(), prefix) {
						allMatchingIDs = append(allMatchingIDs, p.Id)
					}
				}
			}
			offset = p.Id
		}

		if len(res) < 10000 {
			break
		}
	}

	if len(allMatchingIDs) == 0 {
		return 0, nil
	}

	wait := true
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Points{
				Points: &qdrant.PointsIdsList{
					Ids: allMatchingIDs,
				},
			},
		},
		Wait: &wait,
	})
	if err != nil {
		return 0, fmt.Errorf("batch delete of %d points by prefix %q failed: %w", len(allMatchingIDs), prefix, err)
	}

	return len(allMatchingIDs), nil
}

func (s *QdrantStore) searchByChunkType(ctx context.Context, collection string, query SearchQuery, chunkType string) ([]SearchResult, error) {
	limit := normalizeLimit(query.Limit)
	withPayload := true
	resp, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQuery(query.Vector...),
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{matchKeyword("chunk_type", chunkType)},
		},
		Limit:       &limit,
		WithPayload: &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: withPayload}},
	})
	if err != nil {
		return nil, err
	}
	return s.toSearchResults(resp), nil
}

func (s *QdrantStore) toSearchResults(points []*qdrant.ScoredPoint) []SearchResult {
	results := make([]SearchResult, len(points))
	for i, r := range points {
		results[i] = SearchResult{
			Score: r.Score,
			Point: Point{
				ID:      pointIDToString(r.Id),
				Payload: s.payloadToMap(r.Payload),
			},
		}
	}
	return results
}

func matchBool(key string, value bool) *qdrant.Condition {
	return &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: key,
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Boolean{Boolean: value},
				},
			},
		},
	}
}

func matchKeyword(key, value string) *qdrant.Condition {
	return &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: key,
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Keyword{Keyword: value},
				},
			},
		},
	}
}

func buildFilter(filters map[string]interface{}) *qdrant.Filter {
	if len(filters) == 0 {
		return nil
	}
	filter := &qdrant.Filter{}
	for key, val := range filters {
		switch typed := val.(type) {
		case string:
			filter.Must = append(filter.Must, matchKeyword(key, typed))
		case []string:
			cond := make([]*qdrant.Condition, 0, len(typed))
			for _, v := range typed {
				cond = append(cond, matchKeyword(key, v))
			}
			filter.Should = append(filter.Should, cond...)
		default:
			filter.Must = append(filter.Must, matchKeyword(key, fmt.Sprintf("%v", val)))
		}
	}
	return filter
}

func pointIDToString(id *qdrant.PointId) string {
	if id == nil {
		return ""
	}
	if num := id.GetNum(); num != 0 {
		return fmt.Sprintf("%d", num)
	}
	return id.GetUuid()
}

func normalizeLimit(limit int) uint64 {
	if limit <= 0 {
		return 10
	}
	return uint64(limit)
}

// ptr returns a pointer to v. Generic helper used for optional proto fields.
func ptr[T any](v T) *T {
	return &v
}

// ParseQdrantURL parses host and gRPC port from a URL like http://localhost:6333.
// The Qdrant go-client uses gRPC (default 6334), REST port (6333) is ignored.
func ParseQdrantURL(rawURL string) (host string, port int) {
	host = "localhost"
	port = 6334
	if rawURL == "" {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	if h := u.Hostname(); h != "" {
		host = h
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			// If the port is 6333 (REST), we default to 6334 (gRPC) to avoid protocol mismatch.
			// The qdrant-go-client requires gRPC.
			if n != 6333 {
				port = n
			}
		}
	}
	return
}
