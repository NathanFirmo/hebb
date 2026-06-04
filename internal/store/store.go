package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nathan/hebb/internal/config"
	"github.com/nathan/hebb/internal/memory"
)

func init() {
	sqlitevec.Auto()
}

//go:embed schema.sql
var schemaSQL string

type OpenOptions struct {
	Home string
	DB   string
}

type Store struct {
	db *sql.DB
}

type TraceInput struct {
	Kind         memory.TraceKind
	Title        string
	Body         string
	Scope        string
	Source       string
	Valence      float64
	Confidence   float64
	Strength     float64
	Salience     float64
	Status       memory.TraceStatus
	MetadataJSON string
}

type RetrieveOptions struct {
	Query          string
	Kind           string
	Scope          string
	Status         string
	Limit          int
	Vector         []float32
	MinVectorScore float64
}

type RetrievedTrace struct {
	Trace            memory.Trace
	Score            float64
	VectorScore      float64
	FTSScore         float64
	AssociationScore float64
}

type Stats struct {
	Traces        int64
	Entities      int64
	Associations  int64
	PendingEmbeds int64
}

type EntityInfo struct {
	Entity memory.Entity  `json:"entity"`
	Traces []memory.Trace `json:"traces"`
}

func Init(opts OpenOptions) error {
	if opts.Home == "" || opts.DB == "" {
		return fmt.Errorf("home and db path are required")
	}
	if err := os.MkdirAll(opts.Home, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.DB), 0o755); err != nil {
		return err
	}
	s, err := Open(opts)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.SetSetting("hebb_home", opts.Home)
}

func Open(opts OpenOptions) (*Store, error) {
	if opts.Home == "" || opts.DB == "" {
		return nil, fmt.Errorf("home and db path are required")
	}
	if err := os.MkdirAll(opts.Home, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.DB), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", opts.DB+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return err
	}
	if err := s.SetSetting("embedding_model", config.DefaultEmbeddingModel); err != nil {
		return err
	}
	_, _ = s.db.Exec(`INSERT OR IGNORE INTO settings(key, value) VALUES ('schema_version', '1')`)
	return nil
}

func (s *Store) SQLiteVersions() (sqliteVersion, vecVersion string, err error) {
	err = s.db.QueryRow(`SELECT sqlite_version(), vec_version()`).Scan(&sqliteVersion, &vecVersion)
	return sqliteVersion, vecVersion, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at
	`, key, value, now())
	return err
}

func (s *Store) Setting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (s *Store) EmbeddingDimensions() (int, bool, error) {
	value, ok, err := s.Setting("embedding_dimensions")
	if err != nil || !ok {
		return 0, false, err
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

func (s *Store) EnsureVectorTable(dim int) error {
	if dim <= 0 {
		return fmt.Errorf("embedding dimension must be positive")
	}
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS trace_vec USING vec0(embedding float[%d] distance_metric=cosine)`, dim)); err != nil {
		return err
	}
	return s.SetSetting("embedding_dimensions", strconv.Itoa(dim))
}

func (s *Store) CreateTrace(ctx context.Context, input TraceInput, vector []float32) (int64, error) {
	input = normalizeTraceInput(input)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO traces(kind, title, body, scope, source, valence, confidence, strength, salience, status, metadata_json, embedding_pending)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Kind, input.Title, input.Body, input.Scope, input.Source, input.Valence, input.Confidence, input.Strength, input.Salience, input.Status, input.MetadataJSON, len(vector) == 0)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if len(vector) > 0 {
		if err := insertVector(ctx, tx, id, vector); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO trace_events(trace_id, event_type, payload_json) VALUES (?, 'encoded', ?)`, id, `{}`); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) UpsertEntity(ctx context.Context, name, entityType string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("entity name is required")
	}
	if entityType == "" {
		entityType = "concept"
	}
	canonical := strings.ToLower(name)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO entities(name, type, canonical_name, metadata_json)
		VALUES (?, ?, ?, '{}')
		ON CONFLICT(canonical_name, type) DO UPDATE SET name = excluded.name
	`, name, entityType, canonical)
	if err != nil {
		return 0, err
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM entities WHERE canonical_name = ? AND type = ?`, canonical, entityType).Scan(&id)
	return id, err
}

func (s *Store) LinkTraceEntity(ctx context.Context, traceID, entityID int64, role string, confidence float64) error {
	if role == "" {
		role = "mentions"
	}
	if confidence == 0 {
		confidence = 0.7
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO trace_entities(trace_id, entity_id, role, confidence)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(trace_id, entity_id, role) DO UPDATE SET confidence = max(trace_entities.confidence, excluded.confidence)
	`, traceID, entityID, role, confidence)
	return err
}

func (s *Store) EntityInfo(ctx context.Context, name string) (EntityInfo, error) {
	canonical := strings.ToLower(strings.TrimSpace(name))
	var info EntityInfo
	err := s.db.QueryRowContext(ctx, `SELECT id, name, type, canonical_name, metadata_json FROM entities WHERE canonical_name = ? ORDER BY id LIMIT 1`, canonical).
		Scan(&info.Entity.ID, &info.Entity.Name, &info.Entity.Type, &info.Entity.CanonicalName, &info.Entity.MetadataJSON)
	if err != nil {
		return info, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.kind, t.title, t.body, t.scope, t.source, t.valence, t.confidence, t.strength, t.salience, t.status, t.created_at, t.updated_at, t.last_recalled_at, t.recall_count, t.metadata_json
		FROM traces t
		JOIN trace_entities te ON te.trace_id = t.id
		WHERE te.entity_id = ?
		ORDER BY t.updated_at DESC
		LIMIT 50
	`, info.Entity.ID)
	if err != nil {
		return info, err
	}
	defer rows.Close()
	info.Traces, err = scanTraces(rows)
	return info, err
}

func (s *Store) SetTraceVector(ctx context.Context, id int64, vector []float32) error {
	if len(vector) == 0 {
		return fmt.Errorf("vector is empty")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM trace_vec WHERE rowid = ?`, id); err != nil {
		return err
	}
	if err := insertVector(ctx, tx, id, vector); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE traces SET embedding_pending = 0, updated_at = ? WHERE id = ?`, now(), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO trace_events(trace_id, event_type, payload_json) VALUES (?, 'embedded', ?)`, id, `{}`); err != nil {
		return err
	}
	return tx.Commit()
}

func insertVector(ctx context.Context, tx *sql.Tx, id int64, vector []float32) error {
	blob, err := sqlitevec.SerializeFloat32(vector)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO trace_vec(rowid, embedding) VALUES (?, ?)`, id, blob)
	return err
}

func (s *Store) PendingEmbeddingTraces(ctx context.Context, limit int) ([]memory.Trace, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, baseTraceSelect()+` WHERE embedding_pending = 1 AND status <> 'forgotten' ORDER BY updated_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTraces(rows)
}

func (s *Store) Retrieve(ctx context.Context, opts RetrieveOptions) ([]RetrievedTrace, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	candidates := map[int64]*RetrievedTrace{}
	if strings.TrimSpace(opts.Query) != "" {
		fts, err := s.retrieveFTS(ctx, opts)
		if err == nil {
			mergeCandidates(candidates, fts)
		}
	}
	if len(opts.Vector) > 0 {
		vec, err := s.retrieveVector(ctx, opts)
		if err == nil {
			mergeCandidates(candidates, vec)
		}
	}
	if len(candidates) == 0 && strings.TrimSpace(opts.Query) == "" {
		recent, err := s.retrieveRecent(ctx, opts)
		if err != nil {
			return nil, err
		}
		mergeCandidates(candidates, recent)
	}
	results := make([]RetrievedTrace, 0, len(candidates))
	for _, item := range candidates {
		minVectorScore := opts.MinVectorScore
		if minVectorScore == 0 && strings.TrimSpace(opts.Query) != "" && len(opts.Vector) > 0 {
			minVectorScore = 0.60
		}
		if minVectorScore > 0 && item.VectorScore > 0 && item.VectorScore < minVectorScore && item.FTSScore == 0 {
			continue
		}
		item.AssociationScore = s.associationScore(ctx, item.Trace.ID)
		item.Score = 0.40*item.VectorScore + 0.35*item.FTSScore + 0.10*item.AssociationScore + 0.10*strengthSignal(item.Trace) + 0.05*freshnessSignal(item.Trace)
		results = append(results, *item)
	}
	sortRetrieved(results)
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	for _, result := range results {
		_ = s.MarkRecalled(ctx, result.Trace.ID)
	}
	return results, nil
}

func (s *Store) retrieveFTS(ctx context.Context, opts RetrieveOptions) ([]RetrievedTrace, error) {
	where, args := filters("t", opts)
	args = append([]any{quoteFTS(opts.Query)}, args...)
	args = append(args, opts.Limit*3)
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.kind, t.title, t.body, t.scope, t.source, t.valence, t.confidence, t.strength, t.salience, t.status,
		       t.created_at, t.updated_at, t.last_recalled_at, t.recall_count, t.metadata_json,
		       1.0 / (1.0 + bm25(trace_fts)) AS fts_score
		FROM trace_fts
		JOIN traces t ON t.id = trace_fts.rowid
		WHERE trace_fts MATCH ? `+where+`
		ORDER BY bm25(trace_fts)
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetrievedTrace
	for rows.Next() {
		var item RetrievedTrace
		if err := scanTraceWithExtra(rows, &item.Trace, &item.FTSScore); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) retrieveVector(ctx context.Context, opts RetrieveOptions) ([]RetrievedTrace, error) {
	blob, err := sqlitevec.SerializeFloat32(opts.Vector)
	if err != nil {
		return nil, err
	}
	where, args := filters("t", opts)
	args = append([]any{blob, opts.Limit * 3}, args...)
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.kind, t.title, t.body, t.scope, t.source, t.valence, t.confidence, t.strength, t.salience, t.status,
		       t.created_at, t.updated_at, t.last_recalled_at, t.recall_count, t.metadata_json,
		       1.0 / (1.0 + v.distance) AS vector_score
		FROM trace_vec v
		JOIN traces t ON t.id = v.rowid
		WHERE v.embedding MATCH ? AND k = ? `+where+`
		ORDER BY v.distance
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetrievedTrace
	for rows.Next() {
		var item RetrievedTrace
		if err := scanTraceWithExtra(rows, &item.Trace, &item.VectorScore); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) retrieveRecent(ctx context.Context, opts RetrieveOptions) ([]RetrievedTrace, error) {
	where, args := filters("t", opts)
	args = append(args, opts.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.kind, t.title, t.body, t.scope, t.source, t.valence, t.confidence, t.strength, t.salience, t.status, t.created_at, t.updated_at, t.last_recalled_at, t.recall_count, t.metadata_json FROM traces t WHERE t.status <> 'forgotten' `+where+` ORDER BY t.updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	traces, err := scanTraces(rows)
	if err != nil {
		return nil, err
	}
	out := make([]RetrievedTrace, 0, len(traces))
	for _, trace := range traces {
		out = append(out, RetrievedTrace{Trace: trace})
	}
	return out, nil
}

func (s *Store) GetTrace(ctx context.Context, id int64) (memory.Trace, error) {
	row := s.db.QueryRowContext(ctx, baseTraceSelect()+` WHERE id = ?`, id)
	return scanTrace(row)
}

func (s *Store) Associate(ctx context.Context, fromID, toID int64, relation string, strength, confidence float64) (int64, error) {
	if strength == 0 {
		strength = 0.5
	}
	if confidence == 0 {
		confidence = 0.7
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO associations(from_trace_id, to_trace_id, relation, strength, confidence, evidence_count, last_reinforced_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(from_trace_id, to_trace_id, relation) DO UPDATE SET
			strength = min(1.0, associations.strength + 0.1),
			confidence = max(associations.confidence, excluded.confidence),
			evidence_count = associations.evidence_count + 1,
			last_reinforced_at = excluded.last_reinforced_at
	`, fromID, toID, strings.TrimSpace(relation), strength, confidence, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Reinforce(ctx context.Context, id int64, reason string) error {
	return s.traceEventUpdate(ctx, id, "reinforced", reason, `
		UPDATE traces
		SET strength = min(1.0, strength + 0.08),
		    salience = min(1.0, salience + 0.06),
		    confidence = min(1.0, confidence + 0.03),
		    updated_at = ?
		WHERE id = ?
	`)
}

func (s *Store) Inhibit(ctx context.Context, id int64, reason string) error {
	return s.traceEventUpdate(ctx, id, "inhibited", reason, `
		UPDATE traces
		SET status = 'inhibited',
		    salience = max(0.0, salience - 0.25),
		    strength = max(0.0, strength - 0.10),
		    updated_at = ?
		WHERE id = ?
	`)
}

func (s *Store) Forget(ctx context.Context, id int64, soft bool, reason string) error {
	if !soft {
		_, err := s.db.ExecContext(ctx, `DELETE FROM traces WHERE id = ?`, id)
		return err
	}
	return s.traceEventUpdate(ctx, id, "forgotten", reason, `
		UPDATE traces SET status = 'forgotten', updated_at = ? WHERE id = ?
	`)
}

func (s *Store) Decay(ctx context.Context, dryRun bool) (int64, error) {
	if dryRun {
		var count int64
		err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM traces WHERE status = 'active' AND strength > 0`).Scan(&count)
		return count, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE traces
		SET strength = max(0.0, strength - 0.01),
		    salience = max(0.0, salience - 0.005),
		    updated_at = ?
		WHERE status = 'active'
	`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) Consolidate(ctx context.Context, scope string) (int64, error) {
	body := "Consolidated summary placeholder for scope " + scope + "."
	id, err := s.CreateTrace(ctx, TraceInput{
		Kind:       memory.TraceSemanticSummary,
		Title:      "Consolidated memory: " + scope,
		Body:       body,
		Scope:      scope,
		Source:     "hebb consolidate",
		Confidence: 0.6,
		Strength:   0.6,
		Salience:   0.5,
	}, nil)
	if err != nil {
		return 0, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE traces SET status = 'consolidated', updated_at = ? WHERE scope = ? AND status = 'active' AND id <> ?`, now(), scope, id)
	return id, err
}

func (s *Store) MarkRecalled(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE traces
		SET last_recalled_at = ?, recall_count = recall_count + 1, salience = min(1.0, salience + 0.02)
		WHERE id = ?
	`, now(), id)
	return err
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	for _, item := range []struct {
		query string
		dst   *int64
	}{
		{`SELECT count(*) FROM traces`, &stats.Traces},
		{`SELECT count(*) FROM entities`, &stats.Entities},
		{`SELECT count(*) FROM associations`, &stats.Associations},
		{`SELECT count(*) FROM traces WHERE embedding_pending = 1`, &stats.PendingEmbeds},
	} {
		if err := s.db.QueryRowContext(ctx, item.query).Scan(item.dst); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func (s *Store) traceEventUpdate(ctx context.Context, id int64, eventType, reason, updateSQL string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, updateSQL, now(), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	payload, _ := json.Marshal(map[string]string{"reason": reason})
	if _, err := tx.ExecContext(ctx, `INSERT INTO trace_events(trace_id, event_type, payload_json) VALUES (?, ?, ?)`, id, eventType, string(payload)); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeTraceInput(input TraceInput) TraceInput {
	if input.Kind == "" {
		input.Kind = memory.TraceObservation
	}
	if input.Status == "" {
		input.Status = memory.StatusActive
	}
	if input.Confidence == 0 {
		input.Confidence = 0.7
	}
	if input.Strength == 0 {
		input.Strength = 0.5
	}
	if input.Salience == 0 {
		input.Salience = 0.5
	}
	if strings.TrimSpace(input.MetadataJSON) == "" {
		input.MetadataJSON = "{}"
	}
	return input
}

func filters(alias string, opts RetrieveOptions) (string, []any) {
	var clauses []string
	var args []any
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	if opts.Kind != "" {
		clauses = append(clauses, prefix+"kind = ?")
		args = append(args, opts.Kind)
	}
	if opts.Scope != "" {
		clauses = append(clauses, prefix+"scope = ?")
		args = append(args, opts.Scope)
	}
	if opts.Status != "" {
		clauses = append(clauses, prefix+"status = ?")
		args = append(args, opts.Status)
	} else {
		clauses = append(clauses, prefix+"status <> 'forgotten'")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

func mergeCandidates(dst map[int64]*RetrievedTrace, src []RetrievedTrace) {
	for _, item := range src {
		existing := dst[item.Trace.ID]
		if existing == nil {
			copy := item
			dst[item.Trace.ID] = &copy
			continue
		}
		if item.FTSScore > existing.FTSScore {
			existing.FTSScore = item.FTSScore
		}
		if item.VectorScore > existing.VectorScore {
			existing.VectorScore = item.VectorScore
		}
	}
}

func (s *Store) associationScore(ctx context.Context, id int64) float64 {
	var score sql.NullFloat64
	_ = s.db.QueryRowContext(ctx, `SELECT avg(strength * confidence) FROM associations WHERE from_trace_id = ? OR to_trace_id = ?`, id, id).Scan(&score)
	if !score.Valid {
		return 0
	}
	return score.Float64
}

func strengthSignal(trace memory.Trace) float64 {
	return (trace.Salience + trace.Strength + trace.Confidence) / 3.0
}

func freshnessSignal(trace memory.Trace) float64 {
	age := time.Since(trace.UpdatedAt)
	if age <= 0 {
		return 1
	}
	days := age.Hours() / 24
	return 1 / (1 + days/30)
}

func sortRetrieved(items []RetrievedTrace) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Score > items[i].Score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func quoteFTS(query string) string {
	tokens := ftsQueryTokens(query)
	if len(tokens) == 0 {
		return `""`
	}
	var terms []string
	for _, token := range tokens {
		terms = append(terms, token+"*")
	}
	return strings.Join(terms, " AND ")
}

func ftsQueryTokens(query string) []string {
	words := strings.FieldsFunc(normalizeFTSQuery(query), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	seen := map[string]bool{}
	var tokens []string
	for _, word := range words {
		word = stemFTSToken(word)
		if len(word) < 3 || ftsStopWord(word) || seen[word] {
			continue
		}
		seen[word] = true
		tokens = append(tokens, word)
	}
	return tokens
}

func stemFTSToken(token string) string {
	if len(token) <= 5 {
		return token
	}
	token = strings.TrimSuffix(token, "s")
	if len(token) > 5 {
		last := token[len(token)-1]
		if last == 'a' || last == 'e' || last == 'o' {
			token = token[:len(token)-1]
		}
	}
	return token
}

func normalizeFTSQuery(query string) string {
	query = strings.ToLower(query)
	replacements := map[string]string{
		"á": "a", "à": "a", "â": "a", "ã": "a",
		"é": "e", "ê": "e",
		"í": "i",
		"ó": "o", "ô": "o", "õ": "o",
		"ú": "u",
		"ç": "c",
	}
	for from, to := range replacements {
		query = strings.ReplaceAll(query, from, to)
	}
	return query
}

func ftsStopWord(word string) bool {
	switch word {
	case "que", "voce", "sabe", "sobre", "minha", "minhas", "meu", "meus",
		"como", "qual", "quais", "pra", "para", "por", "com", "sem",
		"dos", "das", "uma", "uns", "umas", "isso", "esse", "essa":
		return true
	default:
		return false
	}
}

func baseTraceSelect() string {
	return `SELECT id, kind, title, body, scope, source, valence, confidence, strength, salience, status, created_at, updated_at, last_recalled_at, recall_count, metadata_json FROM traces`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrace(row rowScanner) (memory.Trace, error) {
	var trace memory.Trace
	err := scanTraceColumns(row, &trace)
	return trace, err
}

func scanTraces(rows *sql.Rows) ([]memory.Trace, error) {
	var traces []memory.Trace
	for rows.Next() {
		trace, err := scanTrace(rows)
		if err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	return traces, rows.Err()
}

func scanTraceWithExtra(row rowScanner, trace *memory.Trace, extra *float64) error {
	var createdAt, updatedAt string
	var lastRecalled sql.NullString
	if err := row.Scan(&trace.ID, &trace.Kind, &trace.Title, &trace.Body, &trace.Scope, &trace.Source, &trace.Valence, &trace.Confidence, &trace.Strength, &trace.Salience, &trace.Status, &createdAt, &updatedAt, &lastRecalled, &trace.RecallCount, &trace.MetadataJSON, extra); err != nil {
		return err
	}
	return parseTraceTimes(trace, createdAt, updatedAt, lastRecalled)
}

func scanTraceColumns(row rowScanner, trace *memory.Trace) error {
	var createdAt, updatedAt string
	var lastRecalled sql.NullString
	if err := row.Scan(&trace.ID, &trace.Kind, &trace.Title, &trace.Body, &trace.Scope, &trace.Source, &trace.Valence, &trace.Confidence, &trace.Strength, &trace.Salience, &trace.Status, &createdAt, &updatedAt, &lastRecalled, &trace.RecallCount, &trace.MetadataJSON); err != nil {
		return err
	}
	return parseTraceTimes(trace, createdAt, updatedAt, lastRecalled)
}

func parseTraceTimes(trace *memory.Trace, createdAt, updatedAt string, lastRecalled sql.NullString) error {
	var err error
	trace.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return err
	}
	trace.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return err
	}
	if lastRecalled.Valid && lastRecalled.String != "" {
		t, err := parseTime(lastRecalled.String)
		if err != nil {
			return err
		}
		trace.LastRecalledAt = &t
	}
	return nil
}

func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func MigrationNames() ([]string, error) {
	return []string{"0001_initial.sql"}, nil
}
