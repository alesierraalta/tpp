#!/usr/bin/env bash
# Start a throwaway Postgres (pgvector) whose data lives entirely in tmpfs.
# Zero disk, zero volumes, removed on exit.
#
# Usage:
#   ./ephemeral-pg.sh                       # start, print DSN, wait for Ctrl-C
#   ./ephemeral-pg.sh -- pytest tests/       # start, run the command, tear down
#
# Env overrides: PG_IMAGE, PG_PORT, PG_DB, PG_PASSWORD, TMPFS_SIZE, PG_MEMORY, PG_CPUS
# Port is published on 127.0.0.1 only. PGDATA=/pgdata sits outside every image VOLUME (PG16-PG18+).
set -Eeuo pipefail

PG_IMAGE="${PG_IMAGE:-pgvector/pgvector:pg16}"
PG_PORT="${PG_PORT:-0}"          # 0 = let Docker pick a free port
PG_DB="${PG_DB:-test}"
PG_PASSWORD="${PG_PASSWORD:-test}"
TMPFS_SIZE="${TMPFS_SIZE:-512m}"
NAME="eph-pg-$$"

cleanup() {
  docker rm -f -v "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker run --rm -d --name "$NAME" --label ephemeral=1 \
  -e POSTGRES_PASSWORD="$PG_PASSWORD" \
  -e POSTGRES_DB="$PG_DB" \
  -e PGDATA=/pgdata \
  --tmpfs "/pgdata:rw,size=${TMPFS_SIZE},mode=1777" \
  --memory "${PG_MEMORY:-1g}" --cpus "${PG_CPUS:-2}" \
  -p "127.0.0.1:${PG_PORT}:5432" \
  "$PG_IMAGE" >/dev/null

# Resolve the port Docker actually bound.
PORT="$(docker port "$NAME" 5432/tcp | head -1 | sed 's/.*://')"

# Readiness, never a fixed sleep.
for _ in $(seq 1 60); do
  docker exec "$NAME" pg_isready -h 127.0.0.1 -p 5432 -U postgres -d "$PG_DB" >/dev/null 2>&1 && break
  sleep 0.5
done
docker exec "$NAME" pg_isready -h 127.0.0.1 -p 5432 -U postgres -d "$PG_DB" >/dev/null 2>&1 || {
  echo "postgres did not become ready" >&2
  docker logs "$NAME" >&2
  exit 1
}

DSN="postgresql://postgres:${PG_PASSWORD}@127.0.0.1:${PORT}/${PG_DB}"
echo "DATABASE_URL=${DSN}"

if [ "${1:-}" = "--" ]; then
  shift
  DATABASE_URL="$DSN" "$@"
else
  echo "Ctrl-C to tear down." >&2
  # shellcheck disable=SC2034
  while sleep 3600; do :; done
fi
