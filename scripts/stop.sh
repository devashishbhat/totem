#!/usr/bin/env bash
#
# Stops everything `make start` started: the dashboard, the server, and
# Postgres. Safe to run twice.
#
# Usage: make stop

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
RUN_DIR="$PWD/.logs"

note() { printf '    \033[2m%s\033[0m\n' "$1"; }

printf '\n\033[1m==> Stopping\033[0m\n'

for name in dashboard server; do
  file="$RUN_DIR/$name.pid"
  [[ -f "$file" ]] || continue
  pid="$(cat "$file")"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 10); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    kill -9 "$pid" 2>/dev/null || true
    note "$name (pid $pid)"
  fi
  rm -f "$file"
done

docker compose -f local/docker-compose.yml down
note "the database keeps its data; 'make reset' empties it"
