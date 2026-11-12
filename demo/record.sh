#!/usr/bin/env bash
#
# Records the run that the public demo shows.
#
# Resets the local database, runs both demo sessions, replays the send_email
# decision with the poisoned memory removed, waits for the verdict, and dumps
# the result to local/recorded-run.sql.
#
# This is the one thing in the project that spends money: it calls the model
# for the agent and twice per replay sample. Set TM_DEMO_FAKE_MODEL=true to
# rehearse it for free with the scripted model, though the replay then has
# nothing real to measure.
#
# Usage: make record-demo

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$PWD"
COMPOSE=(docker compose -f local/docker-compose.yml)
SEED="local/recorded-run.sql"
LOG="$(mktemp -t totem-record-server)"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

: "${TM_DATABASE_URL:=postgres://totem:totem@localhost:5432/totem?sslmode=disable}"
: "${TM_INGEST_KEY:?set TM_INGEST_KEY in .env (see .env.example)}"
: "${TM_PROJECT_SALT:?set TM_PROJECT_SALT in .env (see .env.example)}"
: "${TM_ENDPOINT:=http://localhost:8080}"
: "${TM_DEMO_FAKE_MODEL:=false}"
export TM_DATABASE_URL TM_INGEST_KEY TM_PROJECT_SALT TM_ENDPOINT
# The recorder must be able to start a replay, whatever .env says.
export TM_DEMO_MODE=false

if [[ "$TM_DEMO_FAKE_MODEL" != "true" && -z "${OPENAI_API_KEY:-}" ]]; then
  echo "OPENAI_API_KEY is not set. Set it in .env, or run with" >&2
  echo "TM_DEMO_FAKE_MODEL=true to rehearse with the scripted model." >&2
  exit 2
fi

server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

psql_q() {
  "${COMPOSE[@]}" exec -T postgres psql -U totem -d totem -tAc "$1" | tr -d '[:space:]'
}

step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

step "Resetting the local database"
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
"${COMPOSE[@]}" up -d --wait

step "Starting totem-server"
(cd server && go run ./cmd/totem-server serve) >"$LOG" 2>&1 &
server_pid=$!
for _ in $(seq 1 60); do
  if curl -fsS "$TM_ENDPOINT/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! curl -fsS "$TM_ENDPOINT/healthz" >/dev/null 2>&1; then
  echo "the server did not come up; its log is at $LOG" >&2
  exit 1
fi

step "Running both demo sessions"
(cd demo && uv run python crm_agent.py both)

step "Waiting for the lineage worker"
for _ in $(seq 1 60); do
  pending=$(psql_q "select count(*) from events where processed_at is null")
  [[ "$pending" == "0" ]] && break
  sleep 1
done
if [[ "$(psql_q 'select count(*) from events where processed_at is null')" != "0" ]]; then
  echo "events are still unprocessed; the server log is at $LOG" >&2
  exit 1
fi

decision=$(psql_q "select d.id from decisions d
  join alerts a on a.subject_id = d.id and a.kind = 'taint.side_effect_from_untrusted'
  order by a.severity = 'critical' desc, d.occurred_at desc limit 1")
if [[ -z "$decision" ]]; then
  echo "no alerted decision was recorded: the agent did not fall for the poisoned page." >&2
  echo "Run 'make demo' a few times, and tune demo/fixtures/vendor_notice.html" >&2
  echo "and the system prompt until it does." >&2
  exit 1
fi
version=$(psql_q "select da.memory_version_id from decision_attributions da
  where da.decision_id = '$decision' and da.level = 'value_flow'
    and da.memory_version_id is not null limit 1")
if [[ -z "$version" ]]; then
  echo "the decision has no value-flow attribution, so there is nothing to replay." >&2
  exit 1
fi

# The demo run just made a burst of calls. A small provider tier counts
# requests per minute, so give that window time to drain rather than
# spending the first replay samples on rate-limit retries.
step "Letting the provider rate-limit window drain"
sleep "${TM_REPLAY_SETTLE_SECONDS:-65}"

step "Replaying the decision without the poisoned memory"
response=$(curl -sS -X POST "$TM_ENDPOINT/api/v1/replays" \
  -H 'Content-Type: application/json' \
  -d "{\"decision_id\":\"$decision\",\"memory_version_id\":\"$version\",\"samples_per_arm\":0}")
replay=$(printf '%s' "$response" | python3 -c '
import json, sys

try:
    body = json.load(sys.stdin)
except ValueError:
    sys.exit("the server did not answer with JSON")
if "id" not in body:
    sys.exit(body.get("error", "the server refused the replay"))
print(body["id"])
') || {
  echo "the replay could not be started: $response" >&2
  exit 1
}

for _ in $(seq 1 120); do
  status=$(psql_q "select status from replay_experiments where id = '$replay'")
  [[ "$status" == "done" || "$status" == "failed" ]] && break
  sleep 2
done
verdict=$(psql_q "select coalesce(verdict, '') from replay_experiments where id = '$replay'")
status=$(psql_q "select status from replay_experiments where id = '$replay'")
if [[ "$status" != "done" ]]; then
  error=$("${COMPOSE[@]}" exec -T postgres psql -U totem -d totem -tAc \
    "select error from replay_experiments where id = '$replay'")
  echo "the replay did not finish: $status $error" >&2
  exit 1
fi
echo "verdict: $verdict"
if [[ "$verdict" != "strong" && "$verdict" != "moderate" ]]; then
  echo "the replay did not prove the memory mattered (verdict $verdict)." >&2
  echo "The recorded demo is meant to show a proven counterfactual; try again," >&2
  echo "or raise samples_per_arm." >&2
  exit 1
fi

step "Dumping the recorded run"
mkdir -p "$(dirname "$SEED")"
"${COMPOSE[@]}" exec -T postgres pg_dump -U totem -d totem \
  --data-only --no-owner --no-privileges --column-inserts \
  --exclude-table=goose_db_version >"$ROOT/$SEED"

rows=$(grep -c '^INSERT' "$ROOT/$SEED" || true)
model=$(psql_q "select coalesce(model, '') from replay_experiments where id = '$replay'")
cat <<SUMMARY

Recorded.
  seed:     $SEED ($rows rows)
  decision: $decision
  verdict:  $verdict
  model:    ${model:-scripted}

Load it into a fresh database with:
  make seed
SUMMARY
