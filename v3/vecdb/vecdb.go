package vecdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"     // database/sql driver "sqlite"
	_ "modernc.org/sqlite/vec" // vec_distance_cosine
)

// ErrNotFound is returned when a collection does not exist.
var ErrNotFound = errors.New("vecdb: collection not found")

// FileName is the SQLite file of a collection inside its directory.
const FileName = "index.db"

// DB is a directory of collections. It is safe for concurrent use; other
// processes may use the same directory at the same time.
type DB struct {
	dir   string
	mu    sync.Mutex
	conns map[string]*sql.DB
}

// New returns the database rooted at dir. Nothing touches the disk until a
// collection is created.
func New(dir string) *DB {
	return &DB{dir: dir, conns: map[string]*sql.DB{}}
}

// Path is the SQLite file of coll.
func (d *DB) Path(coll string) string { return filepath.Join(d.dir, coll, FileName) }

const schema = `
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS points(
	pk      INTEGER PRIMARY KEY,
	id      TEXT NOT NULL UNIQUE,
	payload BLOB NOT NULL,
	vector  BLOB NOT NULL,
	zero    INTEGER NOT NULL -- 1 for the zero vector, which cosine cannot score
);
CREATE TABLE IF NOT EXISTS fields(
	pk    INTEGER NOT NULL REFERENCES points(pk) ON DELETE CASCADE,
	name  TEXT NOT NULL,
	value TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS fields_lookup ON fields(name, value, pk);
CREATE INDEX IF NOT EXISTS fields_pk ON fields(pk);
`

// validName refuses collection names that would leave dir.
func validName(coll string) error {
	if coll == "" || coll == "." || coll == ".." || strings.ContainsAny(coll, `/\%`+"\x00") {
		return fmt.Errorf("vecdb: invalid collection name %q", coll)
	}
	return nil
}

// conn returns the open handle of coll. Without create, a missing file is
// ErrNotFound and nothing is created.
func (d *DB) conn(coll string, create bool) (*sql.DB, error) {
	if err := validName(coll); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if db, ok := d.conns[coll]; ok {
		return db, nil
	}
	path := d.Path(coll)
	mode := "rw"
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("vecdb: %w", err)
		}
		mode = "rwc"
	} else if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	// _txlock=immediate: a write transaction takes the write lock at BEGIN,
	// so it waits on busy_timeout instead of failing on a lock upgrade.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, d.wrap(coll, err)
	}
	uriPath := filepath.ToSlash(abs)
	if len(uriPath) >= 2 && uriPath[1] == ':' {
		uriPath = "/" + uriPath // A Windows drive is a URI path, not a host.
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=" + mode + "&_txlock=immediate" +
		"&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("vecdb: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		if !create && errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("vecdb: %s: %w", path, err)
	}
	d.conns[coll] = db
	return db, nil
}

// dim reads the dimension of coll; 0 when the collection does not exist.
func dim(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var v string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='dim'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// open returns the handle and dimension of an existing collection.
func (d *DB) open(ctx context.Context, coll string) (*sql.DB, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	db, err := d.conn(coll, false)
	if err != nil {
		return nil, 0, err
	}
	n, err := dim(ctx, db)
	if err != nil {
		return nil, 0, d.wrap(coll, err)
	}
	if n == 0 {
		return nil, 0, ErrNotFound
	}
	return db, n, nil
}

// readTx keeps metadata and points in one snapshot across a concurrent Drop.
func (d *DB) readTx(ctx context.Context, coll string) (*sql.Tx, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	db, err := d.conn(coll, false)
	if err != nil {
		return nil, 0, err
	}
	// modernc uses deferred BEGIN for ReadOnly, even with _txlock=immediate.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, d.wrap(coll, err)
	}
	n, err := dim(ctx, tx)
	if err != nil || n == 0 {
		tx.Rollback()
		if err != nil {
			return nil, 0, d.wrap(coll, err)
		}
		return nil, 0, ErrNotFound
	}
	return tx, n, nil
}

func (d *DB) wrap(coll string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("vecdb: %s: %w", d.Path(coll), err)
}

// Ensure creates coll when missing. When it exists with another dimension
// its points are dropped and recreated is true.
func (d *DB) Ensure(ctx context.Context, coll string, n int) (recreated bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if n <= 0 {
		return false, fmt.Errorf("vecdb: invalid dimension %d", n)
	}
	db, err := d.conn(coll, true)
	if err != nil {
		return false, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, d.wrap(coll, err)
	}
	defer tx.Rollback()
	old, err := dim(ctx, tx)
	if err != nil {
		return false, d.wrap(coll, err)
	}
	switch {
	case old == n:
		return false, nil
	case old != 0:
		if _, err := tx.ExecContext(ctx, "DELETE FROM fields; DELETE FROM points"); err != nil {
			return false, d.wrap(coll, err)
		}
		recreated = true
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO meta(key, value) VALUES('dim', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.Itoa(n)); err != nil {
		return false, d.wrap(coll, err)
	}
	return recreated, d.wrap(coll, tx.Commit())
}

// Exists reports whether coll exists. It never creates anything.
func (d *DB) Exists(ctx context.Context, coll string) (bool, error) {
	_, _, err := d.open(ctx, coll)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Dim returns the vector dimension of coll, ErrNotFound when it is missing.
func (d *DB) Dim(ctx context.Context, coll string) (int, error) {
	_, n, err := d.open(ctx, coll)
	return n, err
}

// Drop deletes every point of coll and the collection itself. The file
// stays (another process may hold it open; Windows cannot delete it then);
// Exists reports false afterwards. Missing collections are ErrNotFound.
func (d *DB) Drop(ctx context.Context, coll string) error {
	db, _, err := d.open(ctx, coll)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return d.wrap(coll, err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "DELETE FROM meta WHERE key='dim'")
	if err != nil {
		return d.wrap(coll, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // dropped by another process meanwhile
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM fields; DELETE FROM points"); err != nil {
		return d.wrap(coll, err)
	}
	return d.wrap(coll, tx.Commit())
}

// Close closes every open collection. The DB stays usable: a later call
// reopens what it needs.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var errs []error
	for name, db := range d.conns {
		errs = append(errs, db.Close())
		delete(d.conns, name)
	}
	return errors.Join(errs...)
}
