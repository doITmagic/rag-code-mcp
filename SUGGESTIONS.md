# Suggestions

## Incremental indexing resets status to "starting" — ⚠️ Partially addressed

When a single file is re-indexed incrementally, `StartIndexingAsync` previously
overwrote the status file with a brand-new object, discarding `Languages` data.

**Current state (PR #40):**
- The `State` field is now hidden from external JSON output (`json:"-"`), so AI
  consumers no longer see `"starting"` / `"completed"` strings.
- The `Languages` map is now preserved during incremental re-indexing (not wiped).

**Remaining open item:** during incremental re-indexing, `processed` counters reset
to whatever the incremental run reports. The overall `Languages` snapshot from the
last full indexing run is kept, but live progress during the incremental pass may
temporarily show lower counts.
