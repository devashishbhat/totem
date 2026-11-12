-- +goose Up

-- Raw span rows exactly as ingest received them. This table is also the queue:
-- the lineage worker claims unprocessed rows in (occurred_at, seq) order.
create table events (
    seq           bigserial primary key,
    event_id      uuid        not null unique,
    run_id        text        not null,
    op            text        not null,
    occurred_at   timestamptz not null,
    attrs         jsonb       not null,
    received_at   timestamptz not null default now(),
    processed_at  timestamptz,
    attempts      int         not null default 0
);
create index events_unprocessed_idx on events (occurred_at, seq) where processed_at is null;

create table runs (
    id         text primary key,
    agent_id   text        not null,
    scope      text        not null default '',
    started_at timestamptz not null
);

create table sources (
    id           uuid primary key,
    run_id       text        not null,
    kind         text        not null,
    origin       text        not null default '',
    hmac         text        not null,
    content      text        not null default '',
    trust        smallint    not null,
    trust_reason text        not null default '',
    observed_at  timestamptz not null
);
create index sources_run_idx on sources (run_id, observed_at);
create index sources_hmac_idx on sources (hmac);

create table memories (
    id              uuid primary key,
    memory_key      text not null unique,
    status          text not null check (status in ('active', 'deleted')),
    current_version int  not null default 0
);

create table memory_versions (
    id                uuid primary key,
    memory_id         uuid        not null references memories (id),
    version           int         not null,
    hmac              text        not null,
    content           text        not null default '',
    deleted           boolean     not null default false,
    lineage_precision text        not null check (lineage_precision in ('step', 'run')),
    trust_min         smallint    not null,
    taint_source_ids  uuid[]      not null default '{}',
    recorded_at       timestamptz not null,
    superseded_at     timestamptz,
    unique (memory_id, version)
);
create index memory_versions_hmac_idx on memory_versions (hmac);
create index memory_versions_memory_idx on memory_versions (memory_id, version desc);

create table edges (
    id       uuid primary key,
    kind     text not null check (kind in ('DERIVED_FROM', 'SUPERSEDES')),
    from_id  uuid not null,
    to_id    uuid not null,
    to_type  text not null check (to_type in ('source', 'memory_version')),
    unique (kind, from_id, to_id)
);
create index edges_from_idx on edges (from_id);
create index edges_to_idx on edges (to_id);

create table memory_reads (
    id          uuid primary key,
    run_id      text        not null,
    occurred_at timestamptz not null,
    results     jsonb       not null
);
create index memory_reads_run_idx on memory_reads (run_id, occurred_at);

create table llm_calls (
    id          uuid primary key,
    run_id      text        not null,
    model       text        not null default '',
    in_context  jsonb       not null default '[]',
    payload     jsonb       not null default '{}',
    occurred_at timestamptz not null
);
create index llm_calls_run_idx on llm_calls (run_id, occurred_at);

create table decisions (
    id               uuid primary key,
    run_id           text        not null,
    llm_call_id      uuid references llm_calls (id),
    tool             text        not null default '',
    side_effect      boolean     not null default false,
    args             jsonb       not null default '{}',
    trust_min        smallint    not null,
    taint_source_ids uuid[]      not null default '{}',
    occurred_at      timestamptz not null
);
create index decisions_run_idx on decisions (run_id, occurred_at);

create table decision_attributions (
    decision_id       uuid not null references decisions (id),
    memory_version_id uuid references memory_versions (id),
    source_id         uuid references sources (id),
    level             text not null check (level in ('retrieved', 'in_context', 'value_flow', 'counterfactual')),
    detail            jsonb not null default '{}',
    check (memory_version_id is not null or source_id is not null)
);
create unique index decision_attributions_memory_idx
    on decision_attributions (decision_id, memory_version_id, level)
    where memory_version_id is not null;
create unique index decision_attributions_source_idx
    on decision_attributions (decision_id, source_id, level)
    where source_id is not null;

create table alerts (
    id         uuid primary key,
    kind       text        not null,
    severity   text        not null,
    subject_id uuid        not null,
    run_id     text        not null default '',
    title      text        not null,
    evidence   jsonb       not null default '{}',
    created_at timestamptz not null,
    unique (kind, subject_id)
);
create index alerts_created_idx on alerts (created_at desc);

create table replay_experiments (
    id                       uuid primary key,
    decision_id              uuid        not null references decisions (id),
    target_memory_version_id uuid        not null references memory_versions (id),
    samples_per_arm          int         not null,
    model                    text        not null default '',
    baseline_flip            real,
    variant_flip             real,
    effect                   real,
    ci_low                   real,
    ci_high                  real,
    verdict                  text,
    status                   text        not null,
    error                    text        not null default '',
    created_at               timestamptz not null,
    finished_at              timestamptz
);
create index replay_experiments_decision_idx on replay_experiments (decision_id, created_at desc);

create table replay_samples (
    experiment_id   uuid    not null references replay_experiments (id),
    arm             text    not null check (arm in ('baseline', 'variant')),
    idx             int     not null,
    output          jsonb   not null default '{}',
    matches_baseline boolean not null default false,
    primary key (experiment_id, arm, idx)
);

-- +goose Down
drop table replay_samples;
drop table replay_experiments;
drop table alerts;
drop table decision_attributions;
drop table decisions;
drop table llm_calls;
drop table memory_reads;
drop table edges;
drop table memory_versions;
drop table memories;
drop table sources;
drop table runs;
drop table events;
