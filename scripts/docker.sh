#!/usr/bin/env bash
# Build the image and drive the compose project.
# db down stops this compose project only. It does not remove containers
# that were started outside the project.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

# The image is built from tracked files only, so ignored and untracked paths
# can never enter the build context.
context_dir=""
cleanup_context() {
	if [[ -n "$context_dir" ]]; then
		rm -rf "$context_dir"
	fi
}
trap cleanup_context EXIT

tracked_context() {
	context_dir=$(mktemp -d)
	git -C "$root" archive --format=tar HEAD | tar -x -C "$context_dir"
}

# Compose supplies a default for every variable, so no .env file is required.
# Bash does not export UID. Compose reads these for the konsolai service user.
export KONSOLAI_UID="$(id -u)"
export KONSOLAI_GID="$(id -g)"

usage() {
	cat <<'EOF'
usage: scripts/docker.sh build [base|full]
       scripts/docker.sh db up|down|logs
       scripts/docker.sh run [args...]
       scripts/docker.sh shell
EOF
	exit 2
}

image_name() {
	case "${KONSOLAI_TARGET:-}" in
	full) printf '%s\n' konsolai:full; return ;;
	base) printf '%s\n' konsolai:dev; return ;;
	esac
	# No explicit target: prefer the base image, fall back to the full one.
	if docker image inspect konsolai:dev >/dev/null 2>&1; then
		printf '%s\n' konsolai:dev
		return
	fi
	if docker image inspect konsolai:full >/dev/null 2>&1; then
		printf '%s\n' konsolai:full
		return
	fi
	printf '%s\n' konsolai:dev
}

# -p fixes the project. A surrounding COMPOSE_PROJECT_NAME cannot retarget it.
compose() {
	docker compose -p konsolai "$@"
}

tty_flag() {
	if [[ -t 0 && -t 1 ]]; then
		printf '%s\n' -it
	else
		printf '%s\n' -T
	fi
}

if [[ $# -lt 1 ]]; then
	usage
fi

cmd=$1
shift

case "$cmd" in
build)
	target=${1:-base}
	case "$target" in
	base)
		tracked_context
		docker build --target base -t konsolai:dev "$context_dir"
		;;
	full)
		tracked_context
		docker build --target full -t konsolai:full "$context_dir"
		;;
	*)
		usage
		;;
	esac
	;;
db)
	sub=${1:-}
	case "$sub" in
	up)
		compose up -d db
		;;
	down)
		compose down
		;;
	logs)
		compose logs db
		;;
	*)
		usage
		;;
	esac
	;;
run)
	export KONSOLAI_IMAGE="${KONSOLAI_IMAGE:-$(image_name)}"
	flag=$(tty_flag)
	# docker compose run --rm konsolai, with the current directory mounted.
	compose --profile app run --rm "$flag" konsolai "$@"
	;;
shell)
	export KONSOLAI_IMAGE="${KONSOLAI_IMAGE:-$(image_name)}"
	flag=$(tty_flag)
	compose --profile app run --rm "$flag" --entrypoint bash konsolai
	;;
*)
	usage
	;;
esac
