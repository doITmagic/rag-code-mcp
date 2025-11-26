package storage

import (
	"context"
	"fmt"

	"github.com/qdrant/go-client/qdrant"
)

// QdrantConfig contains Qdrant-specific configuration
type QdrantConfig struct {
	URL        string
	APIKey     string
	Collection string
}

// QdrantClient provides access to Qdrant vector database
type QdrantClient struct {
	config QdrantConfig
	client *qdrant.Client
}

// NewQdrantClient creates a new Qdrant client
func NewQdrantClient(config QdrantConfig) (*QdrantClient, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("qdrant URL is required")
	}

	// Parse URL to extract host and determine if TLS is needed
	// Expected format: http://localhost:6333 or https://host:6333
	url := config.URL
	useTLS := false

	if len(url) > 8 && url[:8] == "https://" {
		url = url[8:]
		useTLS = true
	} else if len(url) > 7 && url[:7] == "http://" {
		url = url[7:]
	}

	// Extract host (without port)
	host := url
	port := 6334 // Default gRPC port

	// Check if port is specified in URL
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == ':' {
			host = url[:i]
			// Port is specified, but Qdrant SDK expects gRPC port (6334)
			// If REST port 6333 is given, use gRPC port 6334
			port = 6334
			break
		}
	}

	// Create Qdrant client configuration
	qdrantConfig := &qdrant.Config{
		Host:   host,
		Port:   port,
		UseTLS: useTLS,
	}

	// Only set API key if it's not empty
	if config.APIKey != "" {
		qdrantConfig.APIKey = config.APIKey
	}

	// Create Qdrant client - SDK uses gRPC by default
	client, err := qdrant.NewClient(qdrantConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create qdrant client: %w", err)
	}

	return &QdrantClient{
		config: config,
		client: client,
	}, nil
}

// CreateCollection creates a new collection
func (c *QdrantClient) CreateCollection(ctx context.Context, name string, dimension int) error {
	// Check if collection exists
	exists, err := c.client.CollectionExists(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to check collection existence: %w", err)
	}

	if exists {
		return nil // Collection already exists
	}

	// Create collection with vector configuration and LOW indexing threshold
	err = c.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(dimension),
			Distance: qdrant.Distance_Cosine,
		}),
		OptimizersConfig: &qdrant.OptimizersConfigDiff{
			IndexingThreshold: qdrant.PtrOf(uint64(100)), // Index immediately after 100 points (default: 10000)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	return nil
}

// CollectionExists checks if a collection exists in Qdrant
func (c *QdrantClient) CollectionExists(ctx context.Context, name string) (bool, error) {
	return c.client.CollectionExists(ctx, name)
}

// GetCollectionPointCount returns the number of points (documents) in a collection
func (c *QdrantClient) GetCollectionPointCount(ctx context.Context, name string) (uint64, error) {
	collectionInfo, err := c.client.GetCollectionInfo(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("failed to get collection info: %w", err)
	}

	if collectionInfo == nil {
		return 0, nil
	}

	return collectionInfo.GetPointsCount(), nil
}

// DeleteCollection deletes an entire collection (DANGEROUS: removes all points)
func (c *QdrantClient) DeleteCollection(ctx context.Context, name string) error {
	if err := c.client.DeleteCollection(ctx, name); err != nil {
		return fmt.Errorf("failed to delete collection %s: %w", name, err)
	}
	return nil
}

// Upsert inserts or updates vectors
func (c *QdrantClient) Upsert(ctx context.Context, id string, vector []float64, payload map[string]interface{}) error {
	// DEBUG: Check if vector is empty
	if len(vector) == 0 {
		return fmt.Errorf("⚠️ UPSERT CALLED WITH EMPTY VECTOR for id=%s", id)
	}

	// Convert payload to Qdrant format
	qdrantPayload := make(map[string]*qdrant.Value)
	for key, val := range payload {
		qdrantPayload[key] = qdrant.NewValueString(fmt.Sprintf("%v", val))
	}

	// Convert float64 to float32
	vector32 := make([]float32, len(vector))
	for i, v := range vector {
		vector32[i] = float32(v)
	}

	// Create point ID (try numeric first, fallback to string/UUID)
	var pointID *qdrant.PointId
	var numID uint64
	if _, scanErr := fmt.Sscanf(id, "%d", &numID); scanErr == nil {
		pointID = qdrant.NewIDNum(numID)
	} else {
		pointID = qdrant.NewID(id)
	}

	// Upsert point
	_, err := c.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: c.config.Collection,
		Points: []*qdrant.PointStruct{
			{
				Id:      pointID,
				Vectors: qdrant.NewVectors(vector32...),
				Payload: qdrantPayload,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert point: %w", err)
	}

	return nil
}

// Search searches for similar vectors
func (c *QdrantClient) Search(ctx context.Context, vector []float64, limit int) ([]SearchResult, error) {
	// Convert float64 to float32
	vector32 := make([]float32, len(vector))
	for i, v := range vector {
		vector32[i] = float32(v)
	}

	// Search for similar vectors
	searchResult, err := c.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: c.config.Collection,
		Query:          qdrant.NewQuery(vector32...),
		Limit:          qdrant.PtrOf(uint64(limit)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	// Convert results
	results := make([]SearchResult, 0, len(searchResult))
	for _, point := range searchResult {
		payload := make(map[string]interface{})
		for key, val := range point.Payload {
			payload[key] = val.GetStringValue()
		}

		// Extract ID as string
		var idStr string
		if point.Id != nil && point.Id.GetNum() != 0 {
			idStr = fmt.Sprintf("%d", point.Id.GetNum())
		} else if point.Id != nil && point.Id.GetUuid() != "" {
			idStr = point.Id.GetUuid()
		}

		results = append(results, SearchResult{
			ID:      idStr,
			Score:   float64(point.Score),
			Payload: payload,
		})
	}

	return results, nil
}

// SearchCodeOnly searches for similar vectors, excluding markdown documentation chunks
func (c *QdrantClient) SearchCodeOnly(ctx context.Context, vector []float64, limit int) ([]SearchResult, error) {
	// Convert float64 to float32
	vector32 := make([]float32, len(vector))
	for i, v := range vector {
		vector32[i] = float32(v)
	}

	// Search for similar vectors, excluding markdown chunks
	// Markdown chunks have chunk_type="markdown", code chunks have type="class|method|function|etc"
	searchResult, err := c.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: c.config.Collection,
		Query:          qdrant.NewQuery(vector32...),
		Limit:          qdrant.PtrOf(uint64(limit)),
		WithPayload:    qdrant.NewWithPayload(true),
		Filter: &qdrant.Filter{
			MustNot: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_Field{
						Field: &qdrant.FieldCondition{
							Key: "chunk_type",
							Match: &qdrant.Match{
								MatchValue: &qdrant.Match_Keyword{
									Keyword: "markdown",
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search code: %w", err)
	}

	// Convert results
	results := make([]SearchResult, 0, len(searchResult))
	for _, point := range searchResult {
		payload := make(map[string]interface{})
		for key, val := range point.Payload {
			payload[key] = val.GetStringValue()
		}

		// Extract ID as string
		var idStr string
		if point.Id != nil && point.Id.GetNum() != 0 {
			idStr = fmt.Sprintf("%d", point.Id.GetNum())
		} else if point.Id != nil && point.Id.GetUuid() != "" {
			idStr = point.Id.GetUuid()
		}

		results = append(results, SearchResult{
			ID:      idStr,
			Score:   float64(point.Score),
			Payload: payload,
		})
	}

	return results, nil
}

// SearchByNameAndType searches for a specific symbol by exact name and type match
// This is useful for find_type_definition where semantic search may not find the exact match
func (c *QdrantClient) SearchByNameAndType(ctx context.Context, name string, types []string) ([]SearchResult, error) {
	// Build type conditions
	typeConditions := make([]*qdrant.Condition, 0, len(types))
	for _, t := range types {
		typeConditions = append(typeConditions, &qdrant.Condition{
			ConditionOneOf: &qdrant.Condition_Field{
				Field: &qdrant.FieldCondition{
					Key: "type",
					Match: &qdrant.Match{
						MatchValue: &qdrant.Match_Keyword{
							Keyword: t,
						},
					},
				},
			},
		})
	}

	// Scroll with filter for exact name match and type in list
	scrollResult, err := c.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: c.config.Collection,
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_Field{
						Field: &qdrant.FieldCondition{
							Key: "name",
							Match: &qdrant.Match{
								MatchValue: &qdrant.Match_Keyword{
									Keyword: name,
								},
							},
						},
					},
				},
			},
			Should: typeConditions,
		},
		Limit:       qdrant.PtrOf(uint32(10)),
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scroll: %w", err)
	}

	// Convert results
	results := make([]SearchResult, 0, len(scrollResult))
	for _, point := range scrollResult {
		payload := make(map[string]interface{})
		for key, val := range point.Payload {
			payload[key] = val.GetStringValue()
		}

		var idStr string
		if point.Id != nil && point.Id.GetNum() != 0 {
			idStr = fmt.Sprintf("%d", point.Id.GetNum())
		} else if point.Id != nil && point.Id.GetUuid() != "" {
			idStr = point.Id.GetUuid()
		}

		results = append(results, SearchResult{
			ID:      idStr,
			Score:   1.0, // Exact match
			Payload: payload,
		})
	}

	return results, nil
}

// Delete deletes a vector by ID
func (c *QdrantClient) Delete(ctx context.Context, id string) error {
	_, err := c.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: c.config.Collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Points{
				Points: &qdrant.PointsIdsList{
					Ids: []*qdrant.PointId{
						qdrant.NewID(id),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete point: %w", err)
	}

	return nil
}

// DeleteByFilter deletes vectors matching a filter
func (c *QdrantClient) DeleteByFilter(ctx context.Context, key, value string) error {
	_, err := c.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: c.config.Collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: &qdrant.Filter{
					Must: []*qdrant.Condition{
						{
							ConditionOneOf: &qdrant.Condition_Field{
								Field: &qdrant.FieldCondition{
									Key: key,
									Match: &qdrant.Match{
										MatchValue: &qdrant.Match_Keyword{
											Keyword: value,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete points by filter: %w", err)
	}

	return nil
}

// Close closes the Qdrant client connection
func (c *QdrantClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// SearchResult represents a search result
type SearchResult struct {
	ID      string
	Score   float64
	Payload map[string]interface{}
}
