#!/usr/bin/env bash
# Validate the Konsolai commit-message convention on the first line:
#   [TYPE|SIZE] Title in imperative mood
# TYPE:  ADD | UPDATE | FIX | DELETE | MOVE | REFACTOR
# SIZE:  MINOR | MEDIUM | MAJOR
#
# Do not install this automatically. If you want a local hook:
#   ln -sf ../../scripts/check-commit-msg.sh .git/hooks/commit-msg
# Git then runs it as: .git/hooks/commit-msg <path-to-commit-message-file>
set -euo pipefail

if [[ $# -lt 1 ]]; then
	echo "usage: $0 <commit-message-file>" >&2
	exit 2
fi

msg=$(head -n 1 "$1")
if ! printf '%s\n' "$msg" | grep -Eq '^\[(ADD|UPDATE|FIX|DELETE|MOVE|REFACTOR)\|(MINOR|MEDIUM|MAJOR)\] .+'; then
	echo "invalid commit message: $msg" >&2
	echo "expected: [TYPE|SIZE] Title in imperative mood" >&2
	exit 1
fi
