#!/usr/bin/env bash
# Syntax-checks every JavaScript file of the front end (and its tests) with Node.
# LDAP_EXPLORER_REQUIRE_NODE=1 (CI): a missing Node fails instead of skipping.
set -euo pipefail
cd "$(dirname "$0")/.."
if ! command -v node >/dev/null 2>&1; then
  if [ "${LDAP_EXPLORER_REQUIRE_NODE:-}" = 1 ]; then echo "node not found (LDAP_EXPLORER_REQUIRE_NODE=1)"; exit 1; fi
  echo "node not found: skipping the JS syntax check"; exit 0
fi
for f in front/js/*.js front/test/*.js; do
  node --check "$f" || { echo "syntax error in $f"; exit 1; }
done
echo "JS syntax ok"
