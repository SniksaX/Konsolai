#!/usr/bin/env bash
set -euo pipefail

name=konsolai-pg-test
export KONSOLAI_TEST_DATABASE_URL="${KONSOLAI_TEST_DATABASE_URL:-postgres://postgres:ktest@localhost:55433/konsolai_test?sslmode=disable}"

usage() {
	echo "usage: scripts/test-db.sh up|down" >&2
	exit 2
}

# Never touch the user's konsolai-pg (port 55432, volume konsolai-pg).
if [[ "${1:-}" != "up" && "${1:-}" != "down" ]]; then
	usage
fi

if [[ "$1" == "down" ]]; then
	docker rm -f "$name" >/dev/null 2>&1 || true
	exit 0
fi

if docker inspect "$name" >/dev/null 2>&1; then
	if [[ "$(docker inspect -f '{{.State.Running}}' "$name")" == "true" ]]; then
		exit 0
	fi
	docker rm -f "$name" >/dev/null 2>&1 || true
fi

docker run -d --rm --name "$name" --network host \
	-e POSTGRES_PASSWORD=ktest \
	-e POSTGRES_DB=konsolai_test \
	-e PGPORT=55433 \
	pgvector/pgvector:pg17-trixie >/dev/null

for _ in $(seq 1 40); do
	if docker exec "$name" pg_isready -h 127.0.0.1 -p 55433 -U postgres >/dev/null 2>&1; then
		exit 0
	fi
	sleep 0.25
done

echo "scripts/test-db.sh: $name did not become ready" >&2
exit 1
