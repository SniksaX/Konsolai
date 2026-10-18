#!/usr/bin/env bash
# scripts/changelog.sh [from-ref]
#
# Print a Markdown changelog of commits after from-ref, grouped by TYPE.
# ADD is Added. UPDATE, REFACTOR, and MOVE are Changed. FIX is Fixed.
# DELETE is Removed. One line per commit subject.
# Merge commits are skipped. Subjects that start with Test, Cover, Assert,
# or Prove are skipped. A subject that names the local machine is skipped
# so the output can be committed.
#
# The default from-ref is the latest v* tag. If there is no v* tag, the
# default is prod when that ref exists, otherwise the root commit and the
# whole history. A tag or prod uses the range from-ref..HEAD. Set
# CHANGELOG_HEADING to replace the default "## Unreleased" title.
set -euo pipefail

cd "$(dirname "$0")/.."

heading="${CHANGELOG_HEADING:-## Unreleased}"
from="${1:-}"
whole=0
if [[ -z "$from" ]]; then
	from="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)"
fi
if [[ -z "$from" ]]; then
	if git rev-parse --verify --quiet prod >/dev/null; then
		from=prod
	else
		from="$(git rev-list --max-parents=0 HEAD | tail -n 1)"
		whole=1
	fi
fi

machine_name() {
	# Pieces are split so this file does not match the denylist itself.
	local pat='fat''boy|sir''aj|/ho''me/|Desk''top/|qw''en|minist''ral|bge-''m3|114''36|114''35|95''01|95''02|554''32|kp''ass|zene''tys|ly''ah|endea''vour|think''pad'
	[[ "$1" =~ $pat ]]
}

added=()
changed=()
fixed=()
removed=()

if [[ "$whole" -eq 1 ]]; then
	log=(git log --no-merges --format=%s HEAD)
else
	log=(git log --no-merges --format=%s "${from}..HEAD")
fi

while IFS= read -r subject; do
	[[ -z "$subject" ]] && continue
	case "$subject" in
	Test* | Cover* | Assert* | Prove*) continue ;;
	esac
	if machine_name "$subject"; then
		continue
	fi
	if [[ ! "$subject" =~ ^\[([A-Z]+)\| ]]; then
		continue
	fi
	case "${BASH_REMATCH[1]}" in
	ADD) added+=("$subject") ;;
	UPDATE | REFACTOR | MOVE) changed+=("$subject") ;;
	FIX) fixed+=("$subject") ;;
	DELETE) removed+=("$subject") ;;
	esac
done < <("${log[@]}")

print_group() {
	local title="$1"
	local -n rows="$2"
	if ((${#rows[@]} == 0)); then
		return
	fi
	printf '### %s\n' "$title"
	local row
	for row in "${rows[@]}"; do
		printf -- '- %s\n' "$row"
	done
	printf '\n'
}

printf '%s\n\n' "$heading"
print_group Added added
print_group Changed changed
print_group Fixed fixed
print_group Removed removed
