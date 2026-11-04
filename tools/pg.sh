#!/usr/bin/env bash
# In-house local PostgreSQL 16 control (no Docker). Usage: tools/pg.sh start|stop|reset|status|createdb NAME
# Overrides: PGBIN (server binaries, default /usr/lib/postgresql/16/bin; on
# macOS/Homebrew e.g. PGBIN=$(brew --prefix postgresql@16)/bin), PGDATA,
# PGPORT (55432), PGSOCK (/tmp), PGDATABASE (bank).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PGBIN="${PGBIN:-/usr/lib/postgresql/16/bin}"
PGDATA="${PGDATA:-$ROOT/.pgdata}"
PGPORT="${PGPORT:-55432}"
PGSOCK="${PGSOCK:-/tmp}"
PGLOG="$PGDATA.log"
DB="${PGDATABASE:-bank}"

as_pg() { # run a command as the postgres OS user when we are root
  if [ "$(id -u)" = "0" ]; then su postgres -c "$*"; else bash -c "$*"; fi
}
ensure_dir() {
  mkdir -p "$PGDATA"
  if [ "$(id -u)" = "0" ]; then chown postgres "$PGDATA"; chmod 700 "$PGDATA"; touch "$PGLOG"; chown postgres "$PGLOG"; fi
  # As root the server runs as the postgres user, which must be able to
  # traverse to PGDATA. Otherwise the server runs as the caller and no
  # permissions are touched.
  if [ "$(id -u)" = "0" ]; then
    local d="$PGDATA"; while [ "$d" != "/" ]; do chmod o+x "$d" 2>/dev/null || true; d="$(dirname "$d")"; done
  fi
}
url() { echo "postgres://postgres@localhost:$PGPORT/$DB?sslmode=disable"; }
running() { as_pg "$PGBIN/pg_ctl -D '$PGDATA' status" >/dev/null 2>&1; }

case "${1:-}" in
  start)
    ensure_dir
    if [ ! -f "$PGDATA/PG_VERSION" ]; then
      as_pg "$PGBIN/initdb -D '$PGDATA' -A trust -U postgres" >/dev/null
    fi
    if ! running; then
      as_pg "$PGBIN/pg_ctl -D '$PGDATA' -o '-p $PGPORT -k $PGSOCK -c listen_addresses=localhost' -l '$PGLOG' -w start" >/dev/null
    fi
    for i in $(seq 1 30); do psql -h "$PGSOCK" -p "$PGPORT" -U postgres -c 'select 1' >/dev/null 2>&1 && break; sleep 0.5; done
    psql -h "$PGSOCK" -p "$PGPORT" -U postgres -tAc "select 1 from pg_database where datname='$DB'" | grep -q 1 || \
      psql -h "$PGSOCK" -p "$PGPORT" -U postgres -qc "create database \"$DB\""
    echo "DATABASE_URL=$(url)"
    ;;
  stop)
    if running; then as_pg "$PGBIN/pg_ctl -D '$PGDATA' -m fast -w stop" >/dev/null; fi
    echo "stopped"
    ;;
  reset)
    "$0" stop >/dev/null || true
    rm -rf "$PGDATA"
    "$0" start
    ;;
  status)
    if running; then echo "running on port $PGPORT ($(url))"; else echo "not running"; exit 1; fi
    ;;
  createdb)
    name="${2:?database name}"
    psql -h "$PGSOCK" -p "$PGPORT" -U postgres -tAc "select 1 from pg_database where datname='$name'" | grep -q 1 || \
      psql -h "$PGSOCK" -p "$PGPORT" -U postgres -qc "create database \"$name\""
    echo "postgres://postgres@localhost:$PGPORT/$name?sslmode=disable"
    ;;
  url) url ;;
  *) echo "usage: $0 start|stop|reset|status|createdb NAME|url" >&2; exit 2 ;;
esac
