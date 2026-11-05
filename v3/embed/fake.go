package embed

import (
	"context"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sync/atomic"
)

// Fake is a deterministic in-memory Embedder for tests: the vector for a text
// is a normalized pseudo-random vector seeded by the text's hash.
type Fake struct {
	Dim  int          // vector size; 0 means 8
	Fail atomic.Int32 // Embed fails this many more calls with ErrUnavailable
}

func (f *Fake) dim() int {
	if f.Dim <= 0 {
		return 8
	}
	return f.Dim
}

// Embed returns one vector per text.
func (f *Fake) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.Fail.Add(-1) >= 0 { // counts down past zero; only the first N calls fail
		return nil, ErrUnavailable
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.Vector(t)
	}
	return out, nil
}

// Vector is the deterministic unit vector for text.
func (f *Fake) Vector(text string) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text))
	r := rand.New(rand.NewPCG(h.Sum64(), 0))
	v := make([]float32, f.dim())
	var norm float64
	for i := range v {
		x := r.Float64()*2 - 1
		v[i] = float32(x)
		norm += x * x
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

// Dimension returns Dim.
func (f *Fake) Dimension(context.Context) (int, error) { return f.dim(), nil }

// Model returns "fake".
func (f *Fake) Model() string { return "fake" }

var (
	_ Embedder = (*Fake)(nil)
	_ Embedder = (*Ollama)(nil)
)
