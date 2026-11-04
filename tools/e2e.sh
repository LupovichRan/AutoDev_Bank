#!/usr/bin/env bash
# Playwright end-to-end suite against a real `bank serve` and PostgreSQL.
#
# Usage: tools/e2e.sh [extra `playwright test` args]   (or ./dev e2e)
#
# Environment:
#   E2E_PORT           port for the server (default 8080)
#   E2E_DATABASE_URL   database to (re)create and use; default: DATABASE_URL
#                      with its database name replaced by E2E_DB_NAME
#   E2E_DB_NAME        default <database in DATABASE_URL>_e2e (must end _e2e[_suffix])
#   E2E_SKIP_REFRESH=1 no live open-data fetch (dead proxy): fixtures only
#
# The e2e database is dropped and recreated on every run. The server runs
# with BANK_DEV_TEST_LOGIN=1 (dev-login / dev-stepup routes) — test only.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

E2E_PORT="${E2E_PORT:-8080}"
BASE_URL_DEFAULT="${DATABASE_URL:-postgres://postgres@localhost:55432/bank?sslmode=disable}"

# with_db URL NAME: URL with its database path segment replaced by NAME.
with_db() {
  local url="$1" name="$2" query="" base
  if [[ "$url" == *\?* ]]; then query="?${url#*\?}"; fi
  base="${url%%\?*}"
  echo "${base%/*}/${name}${query}"
}

# Default name: <database in DATABASE_URL>_e2e.
default_name="${BASE_URL_DEFAULT%%\?*}"
default_name="${default_name##*/}_e2e"
E2E_DB_NAME="${E2E_DB_NAME:-$default_name}"
E2E_DATABASE_URL="${E2E_DATABASE_URL:-$(with_db "$BASE_URL_DEFAULT" "$E2E_DB_NAME")}"
DB_NAME="${E2E_DATABASE_URL%%\?*}"
DB_NAME="${DB_NAME##*/}"
ADMIN_URL="$(with_db "$E2E_DATABASE_URL" postgres)"
if [[ ! "$DB_NAME" =~ ^[A-Za-z0-9_]+$ ]]; then
  echo "e2e: refusing unusual database name '$DB_NAME'" >&2
  exit 2
fi
# The e2e database is dropped on every run: it must look like a throwaway
# (…_e2e or …_e2e_<suffix>) and must not be the application database.
if [[ ! "$DB_NAME" =~ _e2e(_[A-Za-z0-9_]+)?$ ]]; then
  echo "e2e: refusing to drop '$DB_NAME': name must end with _e2e or _e2e_<suffix>" >&2
  exit 2
fi
APP_DB_NAME="${DATABASE_URL:-}"
APP_DB_NAME="${APP_DB_NAME%%\?*}"
APP_DB_NAME="${APP_DB_NAME##*/}"
if [ -n "$APP_DB_NAME" ] && [ "$DB_NAME" = "$APP_DB_NAME" ]; then
  echo "e2e: refusing to drop '$DB_NAME': it is the database named in DATABASE_URL" >&2
  exit 2
fi

log() { echo "e2e: $*" >&2; }

BASE_URL="http://localhost:$E2E_PORT"
# Anything accepting TCP connections on the port (not only a healthy bank) is a conflict.
if curl -sf "$BASE_URL/healthz" >/dev/null 2>&1 || (exec 3<>"/dev/tcp/127.0.0.1/$E2E_PORT") 2>/dev/null; then
  log "port $E2E_PORT is already in use; set E2E_PORT to a free port"
  exit 1
fi

# 1. PostgreSQL: use what is reachable (CI service), else start the local one.
if ! psql "$ADMIN_URL" -qtAc 'select 1' >/dev/null 2>&1; then
  log "starting local PostgreSQL"
  tools/pg.sh start >/dev/null
fi
log "recreating database $DB_NAME"
psql "$ADMIN_URL" -q -v ON_ERROR_STOP=1 \
  -c "drop database if exists \"$DB_NAME\" with (force)" \
  -c "create database \"$DB_NAME\""

mkdir -p .e2e
SERVER_PID=""
cleanup() {
  local rc=$?
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  # The web build overwrites the tracked dist/index.html placeholder.
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git checkout -- web/dist/index.html 2>/dev/null || true
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

# 2. Web build first: the Go binary embeds web/dist.
[ -d web/node_modules ] || npm --prefix web ci
log "building web"
if ! npm --prefix web run build >.e2e/web-build.log 2>&1; then
  cat .e2e/web-build.log >&2
  log "web build failed (log: .e2e/web-build.log)"
  exit 1
fi

log "building server"
go build -o .e2e/bank ./cmd/bank

export DATABASE_URL="$E2E_DATABASE_URL"
export BANK_OPENDATA_FIXTURES=1
.e2e/bank migrate
.e2e/bank seed >/dev/null
NO_LIVE=()
if [ "${E2E_SKIP_REFRESH:-}" = "1" ]; then
  # Fixture-only mode: every live fetch fails at once (dead proxy), so the
  # recorded fixtures (BANK_OPENDATA_FIXTURES=1) are the only data source.
  # Applies to the server too, which may refresh in the background.
  log "E2E_SKIP_REFRESH=1: live open-data fetches disabled; fixtures only"
  # Scoped to the bank commands: curl and Playwright keep their environment.
  NO_LIVE=(env HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 https_proxy=http://127.0.0.1:9 http_proxy=http://127.0.0.1:9 NO_PROXY= no_proxy=)
  "${NO_LIVE[@]}" .e2e/bank opendata-refresh 2>.e2e/refresh.log || log "opendata-refresh reported errors (see .e2e/refresh.log); continuing"
else
  log "refreshing open data (up to 120 s; fixtures cover failures)"
  timeout 120 .e2e/bank opendata-refresh 2>.e2e/refresh.log || log "opendata-refresh failed or timed out (see .e2e/refresh.log); continuing"
fi

# 3. Server.
"${NO_LIVE[@]}" env BANK_DEV_TEST_LOGIN=1 BANK_ADDR=":$E2E_PORT" BANK_ORIGIN="$BASE_URL" \
  .e2e/bank serve >.e2e/server.log 2>&1 &
SERVER_PID=$!
log "waiting for $BASE_URL/healthz (server pid $SERVER_PID, log .e2e/server.log)"
for _ in $(seq 1 120); do
  if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    log "server exited early:"; tail -n 50 .e2e/server.log >&2; exit 1
  fi
  sleep 0.5
done
if ! curl -fsS "$BASE_URL/healthz" >/dev/null || ! kill -0 "$SERVER_PID" 2>/dev/null; then
  log "server not healthy, or answered by another process; server log:"
  cat .e2e/server.log >&2
  exit 1
fi

# 4. Playwright. Preinstalled browsers (sandbox) when present; CI installs its own.
if [ -z "${PLAYWRIGHT_BROWSERS_PATH:-}" ] && [ -d /opt/pw-browsers ]; then
  export PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers
fi
export E2E_BASE_URL="$BASE_URL"
set +e
(cd web && npx playwright test "$@")
rc=$?
set -e
[ "$rc" -eq 0 ] || log "playwright failed (rc=$rc); server log: .e2e/server.log, report: web/playwright-report"
exit "$rc"
