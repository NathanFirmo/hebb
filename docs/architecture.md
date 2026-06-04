# Architecture

Hebb is organized around memory operations rather than around storage primitives.

- `encode`: normalizes facts, observations, decisions, preferences and events into memory traces.
- `retrieve`: combines structured filters, FTS5, `sqlite-vec` and associative expansion.
- `associate`: creates and reinforces relationships between traces, entities and episodes.
- `reinforce`: increases strength and salience when a memory is used, confirmed or reappears.
- `inhibit`: lowers priority for noisy, stale or contradicted memories without deleting them.
- `consolidate`: turns episodic memory into semantic memory, merges duplicates and recalibrates associations.
- `decay`: applies configurable gradual weakening.
- `store`: owns SQLite, FTS5, `sqlite-vec`, migrations and transactions.
- `embed`: calls Ollama `/api/embed` with `mxbai-embed-large` by default.
- `mcp`: exposes tools for agents.
- `cli`: exposes human and automation commands.

## Storage

Hebb stores data in one SQLite database at `~/.hebb/hebb.db` by default. `HEBB_HOME` changes the base directory and `HEBB_DB_PATH` changes only the database file.

The intended SQLite stack is `github.com/mattn/go-sqlite3` plus `github.com/asg017/sqlite-vec-go-bindings/cgo`, with all `sqlite-vec` usage isolated in `internal/store`.

## Retrieval

Retrieval combines four signals:

- Structured filters: kind, scope, entity, status, time and source.
- Lexical search: SQLite FTS5 with `bm25`.
- Semantic search: KNN via `sqlite-vec`.
- Associative expansion: strong links, shared entities, co-recall and consolidated summaries.

Explicit queries do not fall back to recent memories. A trace must match FTS, vector search above threshold, or both.

Initial ranking:

```text
score =
  0.40 * vector_score +
  0.35 * fts_score +
  0.10 * association_score +
  0.10 * salience_strength_confidence +
  0.05 * freshness_or_temporal_fit
```
