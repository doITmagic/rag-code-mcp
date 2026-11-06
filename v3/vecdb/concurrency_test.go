package vecdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// Hooks surround real SQLite operations so interleavings do not depend on timing.
type hookConnector struct {
	dsn         string
	beforeBegin func()
	afterDim    func()
}

func (h *hookConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (h *hookConnector) Connect(context.Context) (driver.Conn, error) {
	c, err := h.Driver().Open(h.dsn)
	if err != nil {
		return nil, err
	}
	return &hookConn{Conn: c, hooks: h}, nil
}

type hookConn struct {
	driver.Conn
	hooks *hookConnector
}

func (c *hookConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.hooks.beforeBegin != nil {
		c.hooks.beforeBegin()
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *hookConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err == nil && strings.Contains(query, "SELECT value FROM meta") && c.hooks.afterDim != nil {
		return &hookRows{Rows: r, afterClose: c.hooks.afterDim}, nil
	}
	return r, err
}

type hookRows struct {
	driver.Rows
	afterClose func()
}

func (r *hookRows) Close() error {
	err := r.Rows.Close()
	r.afterClose()
	return err
}

func hookCollection(t *testing.T, d *DB, coll string, beforeBegin, afterDim func()) *sql.DB {
	t.Helper()
	old, err := d.conn(coll, false)
	require.NoError(t, err)
	require.NoError(t, old.Close())
	db := sql.OpenDB(&hookConnector{
		dsn:         "file:" + d.Path(coll) + "?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)",
		beforeBegin: beforeBegin, afterDim: afterDim,
	})
	d.conns[coll] = db
	return db
}

func TestUpsertChecksDimensionInWriteTransaction(t *testing.T) {
	for _, change := range []string{"recreate", "drop"} {
		t.Run(change, func(t *testing.T) {
			a := New(t.TempDir())
			b := New(a.dir)
			defer a.Close()
			defer b.Close()
			_, err := a.Ensure(ctx, "c", 4)
			require.NoError(t, err)
			require.NoError(t, a.Upsert(ctx, "c", []Point{pt("old", make([]float32, 4), nil)}))
			_, err = b.Dim(ctx, "c")
			require.NoError(t, err)

			hookCollection(t, a, "c", func() {
				if change == "drop" {
					require.NoError(t, b.Drop(ctx, "c"))
				} else {
					recreated, err := b.Ensure(ctx, "c", 8)
					require.NoError(t, err)
					require.True(t, recreated)
				}
			}, nil)
			err = a.Upsert(ctx, "c", []Point{pt("stale", make([]float32, 4), nil)})
			if change == "drop" {
				assert.ErrorIs(t, err, ErrNotFound)
			} else {
				assert.ErrorContains(t, err, "vector size")
			}
			var count int
			require.NoError(t, a.conns["c"].QueryRow("SELECT count(*) FROM points").Scan(&count))
			assert.Zero(t, count, "%s must not accept stale points", change)
		})
	}
}

func TestReadsKeepSnapshotAcrossDrop(t *testing.T) {
	for _, operation := range []string{"Search", "Scan"} {
		t.Run(operation, func(t *testing.T) {
			a, coll := seeded(t)
			b := New(a.dir)
			defer b.Close()
			_, err := b.Dim(ctx, coll)
			require.NoError(t, err)
			var once sync.Once
			var dropErr error
			db := hookCollection(t, a, coll, nil, func() {
				once.Do(func() {
					// The reader remains open until this writer finishes.
					wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
					defer cancel()
					dropErr = b.Drop(wctx, coll)
				})
			})
			read := func() ([]Hit, error) {
				if operation == "Search" {
					return a.Search(ctx, coll, []float32{1, 0}, nil, 10)
				}
				return a.Scan(ctx, coll, nil, 10)
			}
			hits, err := read()
			require.NoError(t, dropErr, "a read transaction must not block Drop")
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"a", "b", "c"}, ids(hits), "points and dim must share a snapshot")
			_, err = read()
			assert.ErrorIs(t, err, ErrNotFound, "a new read sees the committed Drop")
			assert.Zero(t, db.Stats().InUse, "read transactions release their connection")
		})
	}
}

func TestReadOnlyTransactionDoesNotBlockWriter(t *testing.T) {
	a, coll := seeded(t)
	b := New(a.dir)
	defer b.Close()
	_, err := b.Dim(ctx, coll)
	require.NoError(t, err)
	db, err := a.conn(coll, false)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	n, err := dim(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = b.Upsert(wctx, coll, []Point{pt("new", []float32{1, 0}, nil)})
	require.NoError(t, err, "writer must finish while the read transaction is still open")
	assert.Less(t, time.Since(start), 300*time.Millisecond)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM points").Scan(&count))
	assert.Equal(t, 3, count, "read transaction keeps its original snapshot")
	require.NoError(t, tx.Rollback())
	hits, err := a.Scan(ctx, coll, nil, 10)
	require.NoError(t, err)
	assert.Len(t, hits, 4)
}
