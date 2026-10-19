#!/usr/bin/env bash
# Rewrite config.example.yaml and .env.example from internal/config/keys.go.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
go run ./internal/config/cmd/gen "$root"
