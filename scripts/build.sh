#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
ver="$(git describe --tags --always --dirty)"
tagargs=()
if [[ -n "${TAGS:-}" ]]; then
	tagargs=(-tags "$TAGS")
fi
go build -trimpath "${tagargs[@]}" -ldflags "-s -w -X main.version=${ver}" -o ~/.local/bin/konsolai ./cmd/konsolai
