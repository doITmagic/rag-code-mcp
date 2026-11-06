package vecdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Point is one stored item. Fields are indexed keyword values; a field may
// hold several values (any of them matches).
type Point struct {
	ID      string
	Vector  []float32
	Payload []byte
	Fields  map[string][]string
}

// Match is true for a point when any value it stores under Field is one of
// Values. A Match without Values matches nothing.
type Match struct {
	Field  string
	Values []string
}

// Cond is an AND of matches; an empty Cond matches every point.
type Cond []Match

// Hit is one Search or Scan result. Score is the cosine similarity for
// Search, 0 for Scan.
type Hit struct {
	ID      string
	Score   float32
	Payload []byte
}

var errEmptyCond = errors.New("vecdb: delete needs a condition")

func encode(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// where renders c as a SQL condition on the points table.
func (c Cond) where() (string, []any) {
	if len(c) == 0 {
		return "1", nil
	}
	parts := make([]string, 0, len(c))
	var args []any
	for _, m := range c {
		if len(m.Values) == 0 {
			parts = append(parts, "0")
			continue
		}
		// IN (subquery) lets SQLite start from the fields index instead of
		// checking every point.
		parts = append(parts, "points.pk IN (SELECT pk FROM fields WHERE name = ? AND value IN (?"+strings.Repeat(",?", len(m.Values)-1)+"))")
		args = append(args, m.Field)
		for _, v := range m.Values {
			args = append(args, v)
		}
	}
	return strings.Join(parts, " AND "), args
}

func checkDim(coll string, got, want int) error {
	if got != want {
		return fmt.Errorf("vecdb: vector size %d, collection %q has %d", got, coll, want)
	}
	return nil
}

// Upsert inserts or replaces points by ID, in one transaction.
func (d *DB) Upsert(ctx context.Context, coll string, pts []Point) error {
	db, n, err := d.open(ctx, coll)
	if err != nil || len(pts) == 0 {
		return err
	}
	for _, p := range pts {
		if err := checkDim(coll, len(p.Vector), n); err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return d.wrap(coll, err)
	}
	defer tx.Rollback()
	up, err := tx.PrepareContext(ctx, "INSERT INTO points(id, payload, vector, zero) VALUES(?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload, vector=excluded.vector, zero=excluded.zero RETURNING pk")
	if err != nil {
		return d.wrap(coll, err)
	}
	defer up.Close()
	clear, err := tx.PrepareContext(ctx, "DELETE FROM fields WHERE pk = ?")
	if err != nil {
		return d.wrap(coll, err)
	}
	defer clear.Close()
	add, err := tx.PrepareContext(ctx, "INSERT INTO fields(pk, name, value) VALUES(?, ?, ?)")
	if err != nil {
		return d.wrap(coll, err)
	}
	defer add.Close()
	for _, p := range pts {
		var pk int64
		payload := p.Payload
		if payload == nil {
			payload = []byte{} // NOT NULL column
		}
		if err := up.QueryRowContext(ctx, p.ID, payload, encode(p.Vector), zero(p.Vector)).Scan(&pk); err != nil {
			return d.wrap(coll, err)
		}
		if _, err := clear.ExecContext(ctx, pk); err != nil {
			return d.wrap(coll, err)
		}
		for name, vals := range p.Fields {
			for _, v := range vals {
				if _, err := add.ExecContext(ctx, pk, name, v); err != nil {
					return d.wrap(coll, err)
				}
			}
		}
	}
	return d.wrap(coll, tx.Commit())
}

// Delete removes the points matching c. An empty c is refused: it would
// wipe the collection.
func (d *DB) Delete(ctx context.Context, coll string, c Cond) error {
	if len(c) == 0 {
		return errEmptyCond
	}
	db, _, err := d.open(ctx, coll)
	if err != nil {
		return err
	}
	w, args := c.where()
	_, err = db.ExecContext(ctx, "DELETE FROM points WHERE "+w, args...)
	return d.wrap(coll, err)
}

// Search returns the limit points matching c nearest to vec by cosine
// similarity, best first, ties by ID. A zero vector scores 0.
func (d *DB) Search(ctx context.Context, coll string, vec []float32, c Cond, limit int) ([]Hit, error) {
	if limit <= 0 {
		return nil, ctx.Err()
	}
	db, n, err := d.open(ctx, coll)
	if err != nil {
		return nil, err
	}
	if err := checkDim(coll, len(vec), n); err != nil {
		return nil, err
	}
	w, args := c.where()
	query := "SELECT id, payload, " + scoreExpr + " AS score FROM points WHERE " + w + " ORDER BY score DESC, id LIMIT ?"
	all := append([]any{zero(vec), encode(vec)}, args...)
	all = append(all, limit)
	rows, err := db.QueryContext(ctx, query, all...)
	if err != nil {
		return nil, d.wrap(coll, err)
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		var score float64
		if err := rows.Scan(&h.ID, &h.Payload, &score); err != nil {
			return nil, d.wrap(coll, err)
		}
		h.Score = float32(score)
		hits = append(hits, h)
	}
	return hits, d.wrap(coll, rows.Err())
}

// scoreExpr is the cosine similarity of the stored vector and the query.
// The first bound parameter is true when the query is the zero vector, the
// second is the query. vec_distance_cosine cannot score a zero vector on
// either side, so those pairs score 0, as the exact formula defines it.
const scoreExpr = "CASE WHEN ? OR zero THEN 0 ELSE 1 - vec_distance_cosine(vector, ?) END"

func zero(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}

// Scan returns up to limit points matching c, ordered by ID, without scores.
func (d *DB) Scan(ctx context.Context, coll string, c Cond, limit int) ([]Hit, error) {
	if limit <= 0 {
		return nil, ctx.Err()
	}
	db, _, err := d.open(ctx, coll)
	if err != nil {
		return nil, err
	}
	w, args := c.where()
	rows, err := db.QueryContext(ctx, "SELECT id, payload FROM points WHERE "+w+" ORDER BY id LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, d.wrap(coll, err)
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Payload); err != nil {
			return nil, d.wrap(coll, err)
		}
		hits = append(hits, h)
	}
	return hits, d.wrap(coll, rows.Err())
}
