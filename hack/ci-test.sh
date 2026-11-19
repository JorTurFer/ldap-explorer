#!/usr/bin/env bash
# What validates a PR: the same `make ci` that every CI job and every developer runs
# (kept as a script only because older notes refer to it).
set -euo pipefail
cd "$(dirname "$0")/.."
exec make ci
