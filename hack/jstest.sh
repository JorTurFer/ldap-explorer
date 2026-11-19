#!/usr/bin/env bash
# Unit tests of the pure JavaScript modules in front/js (node --test, no npm
# dependencies; Node is dev tooling only and is never shipped).
# LDAPNAV_REQUIRE_NODE=1 (CI): a missing Node fails instead of skipping.
set -euo pipefail
cd "$(dirname "$0")/.."
if ! command -v node >/dev/null 2>&1; then
  if [ "${LDAPNAV_REQUIRE_NODE:-}" = 1 ]; then echo "node not found (LDAPNAV_REQUIRE_NODE=1)"; exit 1; fi
  echo "node not found: skipping the JS unit tests"; exit 0
fi
node --test front/test/*.test.js
