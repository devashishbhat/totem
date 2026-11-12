package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const replayCols = `id, decision_id, target_memory_version_id, samples_per_arm, model,
	baseline_flip, variant_flip, effect, ci_low, ci_high, verdict, status, error, created_at, finished_at`

func (s *Store) CreateReplay(ctx context.Context, e ReplayExperiment) error {
	_, err := s.Pool.Exec(ctx, `
		insert into replay_experiments
			(id, decision_id, target_memory_version_id, samples_per_arm, model, status, created_at)
		values ($1, $2, $3, $4, $5, $6, $7)`,
		e.ID, e.DecisionID, e.TargetMemoryVersionID, e.SamplesPerArm, e.Model, e.Status, e.CreatedAt)
	return err
}

func (s *Store) SetReplayStatus(ctx context.Context, id uuid.UUID, status, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		update replay_experiments set status = $2, error = $3,
			finished_at = case when $2 in ('done', 'failed') then now() else finished_at end
		where id = $1`, id, status, errMsg)
	return err
}

// FinishReplay stores the statistics and the verdict.
func (s *Store) FinishReplay(ctx context.Context, id uuid.UUID, r ReplayResult) error {
	_, err := s.Pool.Exec(ctx, `
		update replay_experiments set
			baseline_flip = $2, variant_flip = $3, effect = $4, ci_low = $5, ci_high = $6,
			verdict = $7, status = 'done', model = $8, finished_at = now()
		where id = $1`,
		id, r.BaselineFlip, r.VariantFlip, r.Effect, r.CILow, r.CIHigh, r.Verdict, r.Model)
	return err
}

// ReplayResult is the outcome the replay runner computes.
type ReplayResult struct {
	BaselineFlip float64
	VariantFlip  float64
	Effect       float64
	CILow        float64
	CIHigh       float64
	Verdict      string
	Model        string
}

func (s *Store) InsertReplaySample(ctx context.Context, smp ReplaySample) error {
	_, err := s.Pool.Exec(ctx, `
		insert into replay_samples (experiment_id, arm, idx, output, matches_baseline)
		values ($1, $2, $3, $4, $5)
		on conflict (experiment_id, arm, idx) do update set
			output = excluded.output, matches_baseline = excluded.matches_baseline`,
		smp.ExperimentID, smp.Arm, smp.Idx, jsonOr(smp.Output, "{}"), smp.MatchesBaseline)
	return err
}

func (s *Store) MarkSampleMatches(ctx context.Context, experimentID uuid.UUID, arm string, idx int, matches bool) error {
	_, err := s.Pool.Exec(ctx,
		`update replay_samples set matches_baseline = $4 where experiment_id = $1 and arm = $2 and idx = $3`,
		experimentID, arm, idx, matches)
	return err
}

func ReplayByID(ctx context.Context, q Queryer, id uuid.UUID) (ReplayExperiment, error) {
	rows, err := q.Query(ctx, `select `+replayCols+` from replay_experiments where id = $1`, id)
	if err != nil {
		return ReplayExperiment{}, err
	}
	list, err := collectReplays(rows)
	if err != nil {
		return ReplayExperiment{}, err
	}
	if len(list) == 0 {
		return ReplayExperiment{}, ErrNotFound
	}
	return list[0], nil
}

func ReplaysForDecision(ctx context.Context, q Queryer, decisionID uuid.UUID) ([]ReplayExperiment, error) {
	rows, err := q.Query(ctx,
		`select `+replayCols+` from replay_experiments where decision_id = $1 order by created_at desc`, decisionID)
	if err != nil {
		return nil, err
	}
	return collectReplays(rows)
}

func collectReplays(rows pgx.Rows) ([]ReplayExperiment, error) {
	defer rows.Close()
	var out []ReplayExperiment
	for rows.Next() {
		var e ReplayExperiment
		if err := rows.Scan(&e.ID, &e.DecisionID, &e.TargetMemoryVersionID, &e.SamplesPerArm, &e.Model,
			&e.BaselineFlip, &e.VariantFlip, &e.Effect, &e.CILow, &e.CIHigh, &e.Verdict,
			&e.Status, &e.Error, &e.CreatedAt, &e.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func ReplaySamples(ctx context.Context, q Queryer, experimentID uuid.UUID) ([]ReplaySample, error) {
	rows, err := q.Query(ctx, `
		select experiment_id, arm, idx, output, matches_baseline
		from replay_samples where experiment_id = $1 order by arm, idx`, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplaySample
	for rows.Next() {
		var smp ReplaySample
		if err := rows.Scan(&smp.ExperimentID, &smp.Arm, &smp.Idx, &smp.Output, &smp.MatchesBaseline); err != nil {
			return nil, err
		}
		out = append(out, smp)
	}
	return out, rows.Err()
}

// PendingReplays returns experiments left running by a restart, so the server
// can fail them instead of leaving the dashboard spinning.
func (s *Store) PendingReplays(ctx context.Context, olderThan time.Duration) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `
		select id from replay_experiments
		where status in ('pending', 'running') and created_at < now() - $1::interval`,
		olderThan.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Counts backs the overview numbers.
func (s *Store) Counts(ctx context.Context) (runs, decisions, memories int, err error) {
	err = s.Pool.QueryRow(ctx, `
		select (select count(*) from runs),
		       (select count(*) from decisions),
		       (select count(*) from memories)`).Scan(&runs, &decisions, &memories)
	return
}

// RunsNewest lists runs with the counts the list page shows.
func (s *Store) RunsNewest(ctx context.Context, limit int) ([]RunSummary, error) {
	rows, err := s.Pool.Query(ctx, `
		select r.id, r.agent_id, r.scope, r.started_at,
		       (select count(*) from decisions d where d.run_id = r.id),
		       (select count(*) from alerts a where a.run_id = r.id),
		       coalesce((select min(d.trust_min) from decisions d where d.run_id = r.id), 4),
		       coalesce((select s.content from sources s
		                 where s.run_id = r.id and s.kind = 'user_message'
		                 order by s.observed_at, s.id limit 1), '')
		from runs r order by r.started_at desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunSummary
	for rows.Next() {
		var s RunSummary
		if err := rows.Scan(&s.ID, &s.AgentID, &s.Scope, &s.StartedAt,
			&s.Decisions, &s.Alerts, &s.TrustMin, &s.Opening); err != nil {
			return nil, err
		}
		s.Tier = TierName(s.TrustMin)
		out = append(out, s)
	}
	return out, rows.Err()
}

// RunSummary is a row of the runs list.
type RunSummary struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Scope     string    `json:"scope"`
	StartedAt time.Time `json:"started_at"`
	Decisions int       `json:"decisions"`
	Alerts    int       `json:"alerts"`
	TrustMin  int16     `json:"trust_min"`
	Tier      string    `json:"tier"`
	// Opening is the first thing the user said in the run. It is what a
	// person recognises a run by; an id is not.
	Opening string `json:"opening"`
}

func RunByID(ctx context.Context, q Queryer, id string) (Run, error) {
	var r Run
	err := q.QueryRow(ctx, `select id, agent_id, scope, started_at from runs where id = $1`, id).
		Scan(&r.ID, &r.AgentID, &r.Scope, &r.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// MemoryWritesInRun lists the versions recorded during one run, for the timeline.
func MemoryWritesInRun(ctx context.Context, q Queryer, runID string) ([]MemoryVersion, error) {
	rows, err := q.Query(ctx, `
		select v.id, v.memory_id, v.version, v.hmac, v.content, v.deleted, v.lineage_precision,
		       v.trust_min, v.taint_source_ids, v.recorded_at, v.superseded_at, m.memory_key
		from memory_versions v
		join memories m on m.id = v.memory_id
		where v.recorded_at >= (select started_at from runs where id = $1)
		  and exists (select 1 from events e
		              where e.run_id = $1 and e.op = 'memory.write'
		                and e.attrs ->> 'totem.memory.id' = m.memory_key
		                and e.attrs ->> 'totem.hmac' = v.hmac)
		order by v.recorded_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryVersion
	for rows.Next() {
		var v MemoryVersion
		if err := rows.Scan(&v.ID, &v.MemoryID, &v.Version, &v.HMAC, &v.Content, &v.Deleted,
			&v.LineagePrecision, &v.TrustMin, &v.TaintSourceIDs, &v.RecordedAt, &v.SupersededAt,
			&v.MemoryKey); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DecodeJSON is a small helper for API handlers that re-shape stored jsonb.
func DecodeJSON[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}
