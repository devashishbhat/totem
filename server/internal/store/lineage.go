package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a row a caller needs is absent.
var ErrNotFound = errors.New("not found")

// --- runs ---

func UpsertRun(ctx context.Context, tx pgx.Tx, r Run) error {
	_, err := tx.Exec(ctx, `
		insert into runs (id, agent_id, scope, started_at)
		values ($1, $2, $3, $4)
		on conflict (id) do update set
			agent_id = case when excluded.agent_id <> '' then excluded.agent_id else runs.agent_id end,
			scope    = case when excluded.scope    <> '' then excluded.scope    else runs.scope    end,
			started_at = least(runs.started_at, excluded.started_at)`,
		r.ID, r.AgentID, r.Scope, r.StartedAt)
	return err
}

// --- sources ---

func InsertSource(ctx context.Context, tx pgx.Tx, s Source) error {
	_, err := tx.Exec(ctx, `
		insert into sources (id, run_id, kind, origin, hmac, content, trust, trust_reason, observed_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (id) do nothing`,
		s.ID, s.RunID, s.Kind, s.Origin, s.HMAC, s.Content, s.Trust, s.TrustReason, s.ObservedAt)
	return err
}

func SourcesByIDs(ctx context.Context, q Queryer, ids []uuid.UUID) ([]Source, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		select id, run_id, kind, origin, hmac, content, trust, trust_reason, observed_at
		from sources where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	return collectSources(rows)
}

func SourcesInRun(ctx context.Context, q Queryer, runID string) ([]Source, error) {
	rows, err := q.Query(ctx, `
		select id, run_id, kind, origin, hmac, content, trust, trust_reason, observed_at
		from sources where run_id = $1 order by observed_at`, runID)
	if err != nil {
		return nil, err
	}
	return collectSources(rows)
}

func SourceByID(ctx context.Context, q Queryer, id uuid.UUID) (Source, error) {
	list, err := SourcesByIDs(ctx, q, []uuid.UUID{id})
	if err != nil {
		return Source{}, err
	}
	if len(list) == 0 {
		return Source{}, ErrNotFound
	}
	return list[0], nil
}

func collectSources(rows pgx.Rows) ([]Source, error) {
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.RunID, &s.Kind, &s.Origin, &s.HMAC, &s.Content,
			&s.Trust, &s.TrustReason, &s.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- memories ---

// EnsureMemory returns the memory row for a key, creating it if it is new.
func EnsureMemory(ctx context.Context, tx pgx.Tx, key string) (Memory, error) {
	var m Memory
	err := tx.QueryRow(ctx, `
		insert into memories (id, memory_key, status, current_version)
		values ($1, $2, 'active', 0)
		on conflict (memory_key) do update set memory_key = memories.memory_key
		returning id, memory_key, status, current_version`,
		uuid.New(), key).Scan(&m.ID, &m.Key, &m.Status, &m.CurrentVersion)
	return m, err
}

func MemoryByKey(ctx context.Context, q Queryer, key string) (Memory, error) {
	var m Memory
	err := q.QueryRow(ctx,
		`select id, memory_key, status, current_version from memories where memory_key = $1`, key).
		Scan(&m.ID, &m.Key, &m.Status, &m.CurrentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func MemoryByID(ctx context.Context, q Queryer, id uuid.UUID) (Memory, error) {
	var m Memory
	err := q.QueryRow(ctx,
		`select id, memory_key, status, current_version from memories where id = $1`, id).
		Scan(&m.ID, &m.Key, &m.Status, &m.CurrentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

const memoryVersionCols = `id, memory_id, version, hmac, content, deleted, lineage_precision,
	trust_min, taint_source_ids, recorded_at, superseded_at`

// CurrentMemoryVersion returns the live version of a memory.
func CurrentMemoryVersion(ctx context.Context, q Queryer, memoryID uuid.UUID) (MemoryVersion, error) {
	rows, err := q.Query(ctx, `select `+memoryVersionCols+`
		from memory_versions where memory_id = $1 order by version desc limit 1`, memoryID)
	if err != nil {
		return MemoryVersion{}, err
	}
	list, err := collectVersions(rows)
	if err != nil {
		return MemoryVersion{}, err
	}
	if len(list) == 0 {
		return MemoryVersion{}, ErrNotFound
	}
	return list[0], nil
}

func MemoryVersionsOf(ctx context.Context, q Queryer, memoryID uuid.UUID) ([]MemoryVersion, error) {
	rows, err := q.Query(ctx, `select `+memoryVersionCols+`
		from memory_versions where memory_id = $1 order by version`, memoryID)
	if err != nil {
		return nil, err
	}
	return collectVersions(rows)
}

func MemoryVersionsByIDs(ctx context.Context, q Queryer, ids []uuid.UUID) ([]MemoryVersion, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `select `+memoryVersionCols+`
		from memory_versions where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	return collectVersions(rows)
}

func MemoryVersionByID(ctx context.Context, q Queryer, id uuid.UUID) (MemoryVersion, error) {
	list, err := MemoryVersionsByIDs(ctx, q, []uuid.UUID{id})
	if err != nil {
		return MemoryVersion{}, err
	}
	if len(list) == 0 {
		return MemoryVersion{}, ErrNotFound
	}
	return list[0], nil
}

// MemoryVersionByHMAC resolves a fingerprint seen in a read or a prompt back to
// the stored version. The newest matching version wins.
func MemoryVersionByHMAC(ctx context.Context, q Queryer, hmac string) (MemoryVersion, error) {
	rows, err := q.Query(ctx, `select `+memoryVersionCols+`
		from memory_versions where hmac = $1 order by recorded_at desc limit 1`, hmac)
	if err != nil {
		return MemoryVersion{}, err
	}
	list, err := collectVersions(rows)
	if err != nil {
		return MemoryVersion{}, err
	}
	if len(list) == 0 {
		return MemoryVersion{}, ErrNotFound
	}
	return list[0], nil
}

func InsertMemoryVersion(ctx context.Context, tx pgx.Tx, v MemoryVersion) error {
	_, err := tx.Exec(ctx, `
		insert into memory_versions (`+memoryVersionCols+`)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		on conflict (memory_id, version) do nothing`,
		v.ID, v.MemoryID, v.Version, v.HMAC, v.Content, v.Deleted, v.LineagePrecision,
		v.TrustMin, ids(v.TaintSourceIDs), v.RecordedAt, v.SupersededAt)
	return err
}

func SupersedeMemoryVersion(ctx context.Context, tx pgx.Tx, versionID uuid.UUID, at time.Time) error {
	_, err := tx.Exec(ctx,
		`update memory_versions set superseded_at = $2 where id = $1 and superseded_at is null`, versionID, at)
	return err
}

func SetMemoryHead(ctx context.Context, tx pgx.Tx, memoryID uuid.UUID, version int, status string) error {
	_, err := tx.Exec(ctx,
		`update memories set current_version = $2, status = $3 where id = $1`, memoryID, version, status)
	return err
}

func collectVersions(rows pgx.Rows) ([]MemoryVersion, error) {
	defer rows.Close()
	var out []MemoryVersion
	for rows.Next() {
		var v MemoryVersion
		if err := rows.Scan(&v.ID, &v.MemoryID, &v.Version, &v.HMAC, &v.Content, &v.Deleted,
			&v.LineagePrecision, &v.TrustMin, &v.TaintSourceIDs, &v.RecordedAt, &v.SupersededAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- edges ---

func InsertEdge(ctx context.Context, tx pgx.Tx, e Edge) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	_, err := tx.Exec(ctx, `
		insert into edges (id, kind, from_id, to_id, to_type)
		values ($1, $2, $3, $4, $5)
		on conflict (kind, from_id, to_id) do nothing`,
		e.ID, e.Kind, e.FromID, e.ToID, e.ToType)
	return err
}

// EdgesFrom returns the outgoing edges of one node.
func EdgesFrom(ctx context.Context, q Queryer, fromID uuid.UUID) ([]Edge, error) {
	rows, err := q.Query(ctx,
		`select id, kind, from_id, to_id, to_type from edges where from_id = $1`, fromID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.Kind, &e.FromID, &e.ToID, &e.ToType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- reads ---

func InsertMemoryRead(ctx context.Context, tx pgx.Tx, r MemoryRead) error {
	_, err := tx.Exec(ctx, `
		insert into memory_reads (id, run_id, occurred_at, results)
		values ($1, $2, $3, $4) on conflict (id) do nothing`,
		r.ID, r.RunID, r.OccurredAt, r.Results)
	return err
}

func MemoryReadsInRun(ctx context.Context, q Queryer, runID string) ([]MemoryRead, error) {
	rows, err := q.Query(ctx,
		`select id, run_id, occurred_at, results from memory_reads where run_id = $1 order by occurred_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRead
	for rows.Next() {
		var r MemoryRead
		if err := rows.Scan(&r.ID, &r.RunID, &r.OccurredAt, &r.Results); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- llm calls ---

func InsertLLMCall(ctx context.Context, tx pgx.Tx, c LLMCall) error {
	_, err := tx.Exec(ctx, `
		insert into llm_calls (id, run_id, model, in_context, payload, occurred_at)
		values ($1, $2, $3, $4, $5, $6) on conflict (id) do nothing`,
		c.ID, c.RunID, c.Model, jsonOr(c.InContext, "[]"), jsonOr(c.Payload, "{}"), c.OccurredAt)
	return err
}

func LLMCallByID(ctx context.Context, q Queryer, id uuid.UUID) (LLMCall, error) {
	var c LLMCall
	err := q.QueryRow(ctx,
		`select id, run_id, model, in_context, payload, occurred_at from llm_calls where id = $1`, id).
		Scan(&c.ID, &c.RunID, &c.Model, &c.InContext, &c.Payload, &c.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func LLMCallsInRun(ctx context.Context, q Queryer, runID string) ([]LLMCall, error) {
	rows, err := q.Query(ctx, `
		select id, run_id, model, in_context, '{}'::jsonb, occurred_at
		from llm_calls where run_id = $1 order by occurred_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LLMCall
	for rows.Next() {
		var c LLMCall
		if err := rows.Scan(&c.ID, &c.RunID, &c.Model, &c.InContext, &c.Payload, &c.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- decisions ---

func InsertDecision(ctx context.Context, tx pgx.Tx, d Decision) error {
	_, err := tx.Exec(ctx, `
		insert into decisions (id, run_id, llm_call_id, tool, side_effect, args, trust_min, taint_source_ids, occurred_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) on conflict (id) do nothing`,
		d.ID, d.RunID, d.LLMCallID, d.Tool, d.SideEffect, jsonOr(d.Args, "{}"),
		d.TrustMin, ids(d.TaintSourceIDs), d.OccurredAt)
	return err
}

const decisionCols = `id, run_id, llm_call_id, tool, side_effect, args, trust_min, taint_source_ids, occurred_at`

func DecisionByID(ctx context.Context, q Queryer, id uuid.UUID) (Decision, error) {
	var d Decision
	err := q.QueryRow(ctx, `select `+decisionCols+` from decisions where id = $1`, id).
		Scan(&d.ID, &d.RunID, &d.LLMCallID, &d.Tool, &d.SideEffect, &d.Args,
			&d.TrustMin, &d.TaintSourceIDs, &d.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func DecisionsInRun(ctx context.Context, q Queryer, runID string) ([]Decision, error) {
	rows, err := q.Query(ctx, `select `+decisionCols+` from decisions where run_id = $1 order by occurred_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ID, &d.RunID, &d.LLMCallID, &d.Tool, &d.SideEffect, &d.Args,
			&d.TrustMin, &d.TaintSourceIDs, &d.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- attributions ---

func InsertAttribution(ctx context.Context, tx pgx.Tx, a Attribution) error {
	_, err := tx.Exec(ctx, `
		insert into decision_attributions (decision_id, memory_version_id, source_id, level, detail)
		values ($1, $2, $3, $4, $5)
		on conflict do nothing`,
		a.DecisionID, a.MemoryVersionID, a.SourceID, a.Level, jsonOr(a.Detail, "{}"))
	return err
}

func AttributionsOf(ctx context.Context, q Queryer, decisionID uuid.UUID) ([]Attribution, error) {
	rows, err := q.Query(ctx, `
		select decision_id, memory_version_id, source_id, level, detail
		from decision_attributions where decision_id = $1`, decisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attribution
	for rows.Next() {
		var a Attribution
		if err := rows.Scan(&a.DecisionID, &a.MemoryVersionID, &a.SourceID, &a.Level, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- alerts ---

// UpsertAlert raises an alert. The same (kind, subject) is raised once; a
// later evaluation can only escalate its severity, never lower it.
func UpsertAlert(ctx context.Context, tx pgx.Tx, a Alert) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	_, err := tx.Exec(ctx, `
		insert into alerts (id, kind, severity, subject_id, run_id, title, evidence, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		on conflict (kind, subject_id) do update set
			severity = excluded.severity,
			title    = excluded.title,
			evidence = excluded.evidence
		where $9 > `+severityRankExpr("alerts.severity"),
		a.ID, a.Kind, a.Severity, a.SubjectID, a.RunID, a.Title, jsonOr(a.Evidence, "{}"), a.CreatedAt,
		SeverityRank(a.Severity))
	return err
}

// severityRankExpr mirrors SeverityRank inside SQL.
func severityRankExpr(col string) string {
	return `(case ` + col + ` when 'critical' then 4 when 'high' then 3 ` +
		`when 'medium' then 2 when 'low' then 1 else 0 end)`
}

// SeverityRank orders severities so an upsert can only escalate.
func SeverityRank(s string) int {
	switch s {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func AlertsNewest(ctx context.Context, q Queryer, limit int) ([]Alert, error) {
	rows, err := q.Query(ctx, `
		select id, kind, severity, subject_id, run_id, title, evidence, created_at
		from alerts order by created_at desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}

func AlertsForSubjects(ctx context.Context, q Queryer, ids []uuid.UUID) ([]Alert, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		select id, kind, severity, subject_id, run_id, title, evidence, created_at
		from alerts where subject_id = any($1) order by created_at desc`, ids)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}

func collectAlerts(rows pgx.Rows) ([]Alert, error) {
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.Kind, &a.Severity, &a.SubjectID, &a.RunID,
			&a.Title, &a.Evidence, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- misc ---

// Queryer is satisfied by both *pgxpool.Pool and pgx.Tx, so read queries work
// inside the lineage worker's transaction and from the API alike.
type Queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ids keeps a nil slice from reaching a uuid[] column that cannot be null.
func ids(list []uuid.UUID) []uuid.UUID {
	if list == nil {
		return []uuid.UUID{}
	}
	return list
}

func jsonOr(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 {
		return fallback
	}
	return string(raw)
}

// MemoryVersionByKeyHMAC resolves a (memory key, fingerprint) pair, which is
// how the SDK refers to a memory it saw in a prompt or a read result.
func MemoryVersionByKeyHMAC(ctx context.Context, q Queryer, key, hmac string) (MemoryVersion, error) {
	rows, err := q.Query(ctx, `
		select v.id, v.memory_id, v.version, v.hmac, v.content, v.deleted, v.lineage_precision,
		       v.trust_min, v.taint_source_ids, v.recorded_at, v.superseded_at
		from memory_versions v
		join memories m on m.id = v.memory_id
		where m.memory_key = $1 and v.hmac = $2
		order by v.version desc limit 1`, key, hmac)
	if err != nil {
		return MemoryVersion{}, err
	}
	list, err := collectVersions(rows)
	if err != nil {
		return MemoryVersion{}, err
	}
	if len(list) == 0 {
		return MemoryVersion{}, ErrNotFound
	}
	return list[0], nil
}
