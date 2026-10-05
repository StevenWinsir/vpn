#!/usr/bin/env bash
set -euo pipefail
umask 077
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/.runtime" "$ROOT/backend/bin"
if [ ! -f "$ROOT/backend/.env" ] || [ ! -f "$ROOT/frontend/.env" ]; then
 echo 'Missing backend/.env or frontend/.env. Configure them from the examples first.' >&2
 exit 1
fi
(cd "$ROOT/backend" && go build -o bin/api ./cmd/api)
api_pid=''
web_pid=''
cleanup() {
 trap - EXIT INT TERM
 if [ -n "$api_pid" ]; then kill "$api_pid" 2>/dev/null || true; fi
 if [ -n "$web_pid" ]; then kill "$web_pid" 2>/dev/null || true; fi
 wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM
(cd "$ROOT/backend" && export HTTP_ADDR=127.0.0.1:8080 AUTO_MIGRATE=false && exec ./bin/api) >"$ROOT/.runtime/backend.log" 2>&1 &
api_pid=$!
(cd "$ROOT/frontend" && exec bash ../scripts/node.sh node node_modules/next/dist/bin/next dev --hostname localhost) >"$ROOT/.runtime/frontend.log" 2>&1 &
web_pid=$!
echo 'Starting local API and frontend. Open http://localhost:3000 after startup.'
echo 'Logs: .runtime/backend.log and .runtime/frontend.log. Ctrl+C stops this run.'
while kill -0 "$api_pid" 2>/dev/null && kill -0 "$web_pid" 2>/dev/null; do sleep 2; done
echo 'A service exited. Review .runtime logs for the cause.' >&2
exit 1
