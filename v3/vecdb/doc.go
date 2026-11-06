// Package vecdb is a small embedded vector database: named collections of
// points (an ID, a vector, an opaque payload and indexed keyword fields)
// with exact cosine search and keyword filters.
//
// Each collection is one SQLite file, <dir>/<collection>/index.db, written
// through modernc.org/sqlite and modernc.org/sqlite/vec (sqlite-vec
// transpiled to Go), so the package builds with CGO_ENABLED=0 on every
// platform Go supports there.
//
// Guarantees, all inherited from SQLite in WAL mode:
//   - a crash or kill mid-write leaves the last committed state;
//   - several processes may open the same collection: readers see every
//     committed write, writers wait for each other (busy timeout);
//   - nothing is cached in the process beyond SQLite's page cache.
//
// Limits: search is exact (every candidate is scored), which stays in the
// tens of milliseconds up to about 100,000 points of 768 dimensions; there
// is no approximate index. Filters are exact keyword matches on fields.
//
// The package imports nothing from the rest of this module, so it can move
// to its own repository unchanged.
package vecdb
