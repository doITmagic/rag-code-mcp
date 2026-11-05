package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// Qdrant is the Store backed by a Qdrant server over gRPC.
type Qdrant struct {
	c *qdrant.Client
}

const (
	defaultTimeout = 30 * time.Second
	scrollPage     = 256
	// maxRecvMsg replaces grpc-go's 4 MB default: a page of 256 symbols with
	// unbounded Content, or a large Search, is legitimately bigger than that.
	maxRecvMsg = 256 << 20
)

// Payload keys that get a keyword index, so every Filter field is an indexed
// exact match.
var indexedKeys = []string{
	"id", "language", "file_path", "dir_parts", "name", "qualified_name", "qn_suffixes",
	"module", "kind", "rel_target_id", "rel_target_qualified_name", "rel_target_name",
}

// NewQdrant connects to the server named by rawURL (http://host:6333 style,
// as the REST URL users configure). The REST port 6333, or no port, maps to
// the gRPC port 6334; any other port is taken as an explicit gRPC port. An
// https scheme enables TLS. apiKey, when set, is sent as gRPC metadata. The
// connection is lazy: this never blocks on the server.
func NewQdrant(rawURL, apiKey string) (*Qdrant, error) {
	host, port, tls, err := parseQdrantURL(rawURL)
	if err != nil {
		return nil, err
	}
	c, err := qdrant.NewClient(&qdrant.Config{
		Host: host, Port: port, UseTLS: tls, APIKey: apiKey,
		SkipCompatibilityCheck: true,
		GrpcOptions:            []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsg))},
	})
	if err != nil {
		return nil, fmt.Errorf("store: qdrant client: %w", err)
	}
	return &Qdrant{c: c}, nil
}

func parseQdrantURL(rawURL string) (host string, port int, tls bool, err error) {
	host, port = "localhost", 6334
	if rawURL == "" {
		return host, port, false, nil
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, false, fmt.Errorf("store: qdrant url %q: %w", rawURL, err)
	}
	if h := u.Hostname(); h != "" {
		host = h
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, false, fmt.Errorf("store: qdrant url %q: bad port", rawURL)
		}
		if n != 6333 {
			port = n
		}
	}
	return host, port, u.Scheme == "https", nil
}

// timeout adds the default deadline when ctx has none.
func timeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

// wrap maps a gRPC NotFound to ErrNotFound. The status code is matched on
// the error text so the package does not import grpc directly.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "code = NotFound") {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}

func (q *Qdrant) EnsureCollection(ctx context.Context, name string, dim int) (bool, error) {
	ctx, cancel := timeout(ctx)
	defer cancel()
	exists, err := q.c.CollectionExists(ctx, name)
	if err != nil {
		return false, wrap(err)
	}
	recreated := false
	if exists {
		info, err := q.c.GetCollectionInfo(ctx, name)
		if err != nil {
			return false, wrap(err)
		}
		if int(info.GetConfig().GetParams().GetVectorsConfig().GetParams().GetSize()) == dim {
			return false, nil
		}
		if err := q.c.DeleteCollection(ctx, name); err != nil {
			return false, wrap(err)
		}
		recreated = true
	}
	err = q.c.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size: uint64(dim), Distance: qdrant.Distance_Cosine,
		}),
	})
	if err != nil {
		return recreated, wrap(err)
	}
	for _, key := range indexedKeys {
		_, err := q.c.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: name, FieldName: key,
			FieldType: qdrant.FieldType_FieldTypeKeyword.Enum(),
		})
		if err != nil {
			return recreated, wrap(err)
		}
	}
	return recreated, nil
}

func (q *Qdrant) CollectionExists(ctx context.Context, name string) (bool, error) {
	ctx, cancel := timeout(ctx)
	defer cancel()
	ok, err := q.c.CollectionExists(ctx, name)
	return ok, wrap(err)
}

func (q *Qdrant) VectorSize(ctx context.Context, name string) (int, error) {
	ctx, cancel := timeout(ctx)
	defer cancel()
	info, err := q.c.GetCollectionInfo(ctx, name)
	if err != nil {
		return 0, wrap(err)
	}
	return int(info.GetConfig().GetParams().GetVectorsConfig().GetParams().GetSize()), nil
}

func (q *Qdrant) DeleteCollection(ctx context.Context, name string) error {
	ctx, cancel := timeout(ctx)
	defer cancel()
	// The client reports a missing collection as a plain failure, not a gRPC
	// NotFound, so check first.
	ok, err := q.c.CollectionExists(ctx, name)
	if err != nil {
		return wrap(err)
	}
	if !ok {
		return ErrNotFound
	}
	return wrap(q.c.DeleteCollection(ctx, name))
}

func (q *Qdrant) Upsert(ctx context.Context, collection string, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ctx, cancel := timeout(ctx)
	defer cancel()
	points := make([]*qdrant.PointStruct, 0, len(items))
	for _, it := range items {
		payload, err := toPayload(&it.Symbol)
		if err != nil {
			return err
		}
		points = append(points, &qdrant.PointStruct{
			Id:      qdrant.NewIDUUID(pointID(it.Symbol.ID)),
			Vectors: qdrant.NewVectorsDense(it.Vector),
			Payload: payload,
		})
	}
	_, err := q.c.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection, Points: points, Wait: qdrant.PtrOf(true),
	})
	return wrap(err)
}

func (q *Qdrant) DeleteByFile(ctx context.Context, collection, filePath string) error {
	return q.deleteByFilter(ctx, collection, Filter{FilePath: filePath})
}

// deleteByFilter refuses an empty filter: a nil selector would wipe the
// collection, and an empty path is never a wildcard.
func (q *Qdrant) deleteByFilter(ctx context.Context, collection string, f Filter) error {
	filter := toQdrantFilter(f)
	if filter == nil {
		return errEmptyPath
	}
	ctx, cancel := timeout(ctx)
	defer cancel()
	_, err := q.c.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Points:         qdrant.NewPointsSelectorFilter(filter),
		Wait:           qdrant.PtrOf(true),
	})
	return wrap(err)
}

func (q *Qdrant) Search(ctx context.Context, collection string, vector []float32, f Filter, limit int) ([]Hit, error) {
	if limit <= 0 {
		return nil, nil
	}
	ctx, cancel := timeout(ctx)
	defer cancel()
	res, err := q.c.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQueryDense(vector),
		Filter:         toQdrantFilter(f),
		Limit:          qdrant.PtrOf(uint64(limit)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, wrap(err)
	}
	hits := make([]Hit, 0, len(res))
	for _, p := range res {
		hits = append(hits, Hit{Score: p.GetScore(), Symbol: fromPayload(p.GetPayload())})
	}
	return hits, nil
}

func (q *Qdrant) Scroll(ctx context.Context, collection string, f Filter, limit int) ([]Hit, error) {
	if limit < 0 || limit > MaxScroll {
		limit = MaxScroll
	}
	ctx, cancel := timeout(ctx)
	defer cancel()
	var (
		hits   []Hit
		offset *qdrant.PointId
		filter = toQdrantFilter(f)
	)
	for len(hits) < limit {
		page := min(scrollPage, limit-len(hits))
		res, next, err := q.c.ScrollAndOffset(ctx, &qdrant.ScrollPoints{
			CollectionName: collection,
			Filter:         filter,
			Offset:         offset,
			Limit:          qdrant.PtrOf(uint32(page)),
			WithPayload:    qdrant.NewWithPayload(true),
		})
		if err != nil {
			return nil, wrap(err)
		}
		for _, p := range res {
			hits = append(hits, Hit{Symbol: fromPayload(p.GetPayload())})
		}
		if next == nil || len(res) == 0 {
			break
		}
		offset = next
	}
	return hits, nil
}

func (q *Qdrant) Close() error { return q.c.Close() }

// toQdrantFilter translates f into a Must filter, nil when f is empty.
func toQdrantFilter(f Filter) *qdrant.Filter {
	var must []*qdrant.Condition
	add := func(key, val string) {
		if val != "" {
			must = append(must, qdrant.NewMatchKeyword(key, val))
		}
	}
	add("id", f.ID)
	add("language", f.Language)
	add("file_path", f.FilePath)
	if f.PathPrefix != "" {
		add("dir_parts", filepath.Clean(f.PathPrefix))
	}
	add("name", f.Name)
	add("qualified_name", f.QualifiedName)
	if f.QualifiedSuffix != "" {
		// A suffix that normalizes to "" matches nothing (parser.MatchQualified);
		// qn_suffixes never holds "", so the keyword "" matches no point.
		must = append(must, qdrant.NewMatchKeyword("qn_suffixes", parser.Normalize(f.QualifiedSuffix)))
	}
	add("module", f.Module)
	if len(f.Kinds) > 0 {
		kinds := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			kinds[i] = string(k)
		}
		must = append(must, qdrant.NewMatchKeywords("kind", kinds...))
	}
	add("rel_target_id", f.TargetID)
	add("rel_target_qualified_name", f.TargetQualifiedName)
	add("rel_target_name", f.TargetName)
	if len(must) == 0 {
		return nil
	}
	return &qdrant.Filter{Must: must}
}

// pointID derives a deterministic UUID (version 5 layout) from a symbol ID.
func pointID(symbolID string) string {
	h := sha1.Sum([]byte(symbolID))
	h[6] = (h[6] & 0x0f) | 0x50
	h[8] = (h[8] & 0x3f) | 0x80
	s := hex.EncodeToString(h[:16])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// qnSuffixes lists every suffix of the normalized qualified name on "."
// boundaries, longest first: "a.b.c" -> a.b.c, b.c, c.
func qnSuffixes(norm string) []string {
	var out []string
	for i := 0; i < len(norm); i++ {
		if i == 0 || norm[i-1] == '.' {
			out = append(out, norm[i:])
		}
	}
	return out
}

// dirParts lists every ancestor directory of path, nearest first.
func dirParts(path string) []string {
	var out []string
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		out = append(out, d)
		if d == filepath.Dir(d) {
			return out
		}
	}
}

func strList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func toPayload(s *parser.Symbol) (map[string]*qdrant.Value, error) {
	relations, err := json.Marshal(s.Relations)
	if err != nil {
		return nil, fmt.Errorf("store: relations of %s: %w", s.ID, err)
	}
	metadata, err := json.Marshal(s.Metadata)
	if err != nil {
		return nil, fmt.Errorf("store: metadata of %s: %w", s.ID, err)
	}
	ids, qns, names := make([]string, 0, len(s.Relations)), make([]string, 0, len(s.Relations)), make([]string, 0, len(s.Relations))
	for _, r := range s.Relations {
		ids, qns, names = append(ids, r.TargetID), append(qns, r.TargetQualifiedName), append(names, r.TargetName)
	}
	norm := parser.Normalize(s.QualifiedName)
	m := map[string]any{
		"id":                        s.ID,
		"qualified_name":            s.QualifiedName,
		"qn_suffixes":               strList(qnSuffixes(norm)),
		"name":                      s.Name,
		"kind":                      string(s.Kind),
		"module":                    s.Module,
		"owner":                     s.Owner,
		"signature":                 s.Signature,
		"doc":                       s.Doc,
		"content":                   s.Content,
		"file_path":                 s.FilePath,
		"dir_parts":                 strList(dirParts(s.FilePath)),
		"start_line":                s.StartLine,
		"end_line":                  s.EndLine,
		"language":                  s.Language,
		"exported":                  s.Exported,
		"relations":                 string(relations),
		"metadata":                  string(metadata),
		"rel_target_id":             strList(ids),
		"rel_target_qualified_name": strList(qns),
		"rel_target_name":           strList(names),
	}
	// protobuf rejects invalid UTF-8 anywhere, list items included.
	for k, v := range m {
		switch v := v.(type) {
		case string:
			m[k] = strings.ToValidUTF8(v, "�")
		case []any:
			for i, x := range v {
				v[i] = strings.ToValidUTF8(x.(string), "�")
			}
		}
	}
	p, err := qdrant.TryValueMap(m)
	if err != nil {
		return nil, fmt.Errorf("store: payload of %s: %w", s.ID, err)
	}
	return p, nil
}

func fromPayload(p map[string]*qdrant.Value) parser.Symbol {
	str := func(k string) string { return p[k].GetStringValue() }
	s := parser.Symbol{
		ID:            str("id"),
		QualifiedName: str("qualified_name"),
		Name:          str("name"),
		Kind:          parser.Kind(str("kind")),
		Module:        str("module"),
		Owner:         str("owner"),
		Signature:     str("signature"),
		Doc:           str("doc"),
		Content:       str("content"),
		FilePath:      str("file_path"),
		StartLine:     int(p["start_line"].GetIntegerValue()),
		EndLine:       int(p["end_line"].GetIntegerValue()),
		Language:      str("language"),
		Exported:      p["exported"].GetBoolValue(),
	}
	// Payload we wrote ourselves; a decode failure leaves the field empty.
	_ = json.Unmarshal([]byte(str("relations")), &s.Relations)
	_ = json.Unmarshal([]byte(str("metadata")), &s.Metadata)
	return s
}
