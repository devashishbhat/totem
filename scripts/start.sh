#!/usr/bin/env bash
#
# Brings the whole thing up and drops you into a conversation.
#
# Starts Postgres, applies the migrations, builds anything that is not built
# yet, starts the server and the dashboard in the background, and then runs
# the assistant in the foreground.
#
# Leaving the conversation leaves the dashboard up, because that is when you
# want to look at it. `make stop` stops everything.
#
# Usage: make start

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$PWD"
COMPOSE=(docker compose -f local/docker-compose.yml)
RUN_DIR="$ROOT/.logs"
SERVER_LOG="$RUN_DIR/server.log"
DASHBOARD_LOG="$RUN_DIR/dashboard.log"

step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
note() { printf '    \033[2m%s\033[0m\n' "$1"; }
fail() { printf '\n\033[1mx %s\033[0m\n' "$1" >&2; }

# -- what has to be installed ---------------------------------------------

missing=()
for tool in docker go uv npm; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
if ((${#missing[@]})); then
  fail "not installed: ${missing[*]}"
  cat >&2 <<'NEEDS'

    docker   https://docs.docker.com/get-docker (or Colima)
    go       https://go.dev/dl
    uv       https://docs.astral.sh/uv
    npm      https://nodejs.org

NEEDS
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  fail "Docker is installed but not running. Start it and try again."
  exit 1
fi

# -- configuration ---------------------------------------------------------

if [[ ! -f .env ]]; then
  cp .env.example .env
  step "Created .env from .env.example"
  note "the defaults work locally; you only need to add your OpenAI key"
fi

set -a
# shellcheck disable=SC1091
. ./.env
set +a

: "${TM_DATABASE_URL:=postgres://totem:totem@localhost:5432/totem?sslmode=disable}"
: "${TM_ENDPOINT:=http://localhost:8080}"
: "${TM_DASHBOARD_URL:=http://localhost:5173}"
export TM_DATABASE_URL TM_ENDPOINT TM_DASHBOARD_URL
# Whatever .env says, a local stack you are talking to is not a public demo.
export TM_DEMO_MODE=false

# The scripted model answers from a fixture and calls nothing, so the tour
# needs no key. Only a real conversation does.
: "${TM_DEMO_FAKE_MODEL:=false}"
export TM_DEMO_FAKE_MODEL

if [[ "$TM_DEMO_FAKE_MODEL" != "true" && -z "${OPENAI_API_KEY:-}" ]]; then
  fail "OPENAI_API_KEY is not set, and the assistant needs a model to talk to."
  cat >&2 <<'KEY'

    1. get a key at https://platform.openai.com/api-keys
    2. put it in .env:   OPENAI_API_KEY=sk-...
    3. run this again

    Or see the staged prompt-injection demo first, which needs no key:
        make tour

KEY
  exit 2
fi

mkdir -p "$RUN_DIR"

# -- probes ----------------------------------------------------------------

# curl rather than /dev/tcp: vite listens on localhost, which on macOS is
# ::1 only, and a hardcoded 127.0.0.1 probe would never answer.
listening() { curl -fsS -o /dev/null "http://localhost:$1/" >/dev/null 2>&1; }
healthy() { curl -fsS "$TM_ENDPOINT/healthz" >/dev/null 2>&1; }

await() {
  local what="$1" check="$2" seconds="$3"
  for _ in $(seq 1 "$seconds"); do
    if eval "$check"; then return 0; fi
    sleep 1
  done
  fail "$what did not come up within ${seconds}s"
  return 1
}

# Until the stack is up, a failure half way through should not leave pieces of
# it running. Once it is up the trap is cleared: from then on the processes are
# meant to outlive this script, and `make stop` is what ends them.
server_pid=""
dashboard_pid=""
cleanup_partial() {
  for pid in "$server_pid" "$dashboard_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
}
trap cleanup_partial EXIT

# -- build -----------------------------------------------------------------

step "Database"
"${COMPOSE[@]}" up -d --wait
(cd server && go run ./cmd/totem-server migrate)

if [[ ! -f sdk/python/src/totem_ai/_core.abi3.so ]]; then
  step "Building the Rust core into the SDK (one time, a minute or two)"
  (cd sdk/python && uv sync --all-extras && uv run maturin develop --release)
fi

step "Dependencies"
(cd demo && uv sync --quiet)
if [[ ! -d dashboard/node_modules ]]; then
  (cd dashboard && npm install --silent)
fi

# -- run -------------------------------------------------------------------

if healthy; then
  step "Server already running on $TM_ENDPOINT"
else
  step "Server"
  (cd server && go build -o "$ROOT/server/bin/totem-server" ./cmd/totem-server)
  # The built binary is started directly rather than through `go run`, so the
  # pid written below is the server itself and stopping it stops it.
  "$ROOT/server/bin/totem-server" serve >"$SERVER_LOG" 2>&1 &
  server_pid=$!
  await "the server" healthy 60 || { note "its log: $SERVER_LOG"; exit 1; }
  echo "$server_pid" >"$RUN_DIR/server.pid"
  note "$TM_ENDPOINT  (log: $SERVER_LOG)"
fi

if listening 5173; then
  step "Dashboard already running on $TM_DASHBOARD_URL"
else
  step "Dashboard"
  # exec, so $! is the vite process and not a wrapper that would orphan it.
  (cd dashboard && exec ./node_modules/.bin/vite --port 5173 --strictPort) \
    >"$DASHBOARD_LOG" 2>&1 &
  dashboard_pid=$!
  await "the dashboard" "listening 5173" 60 || { note "its log: $DASHBOARD_LOG"; exit 1; }
  echo "$dashboard_pid" >"$RUN_DIR/dashboard.pid"
  note "$TM_DASHBOARD_URL  (log: $DASHBOARD_LOG)"
fi

trap - EXIT

status=0
if [[ "$TM_DEMO_FAKE_MODEL" == "true" ]]; then
  step "The staged attack"
  note "an assistant reads a poisoned page, writes down what it says, and later emails the attacker"
  (cd demo && uv run python crm_agent.py both) || status=$?
  printf '\n\033[1m==> Open %s\033[0m\n' "$TM_DASHBOARD_URL"
  note "start at the alert on the overview and follow it back to the page that caused it"
else
  printf '\n\033[1m==> Open %s, then talk to the assistant below.\033[0m\n' "$TM_DASHBOARD_URL"
  note "everything it reads, remembers, recalls and decides shows up there as you go"
  (cd demo && uv run python chat.py "$@") || status=$?
  printf '\n\033[1m==> The dashboard is still up at %s\033[0m\n' "$TM_DASHBOARD_URL"
fi

note "look around, then run 'make stop' when you are done"
exit "$status"
