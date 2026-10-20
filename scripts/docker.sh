#!/usr/bin/env bash
# Build the image and drive the compose project.
# db down stops this compose project only. It does not remove containers
# that were started outside the project.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

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
	case "${KONSOLAI_TARGET:-base}" in
	full) printf '%s\n' konsolai:full ;;
	base) printf '%s\n' konsolai:dev ;;
	*)
		echo "scripts/docker.sh: KONSOLAI_TARGET must be base or full" >&2
		exit 2
		;;
	esac
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
		docker build --target base -t konsolai:dev "$root"
		;;
	full)
		docker build --target full -t konsolai:full "$root"
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
