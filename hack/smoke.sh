#!/usr/bin/env bash
# Smoke test of an already built binary: starts it with -no-open, checks that
# the API returns 403 without a token (or with a wrong one) and 200 with
# X-Token, and that the UI is served with a CSP.
# Usage: hack/smoke.sh ./ldap-explorer   (or ldap-explorer.exe, or the one inside the .app)
set -euo pipefail
bin="${1:?usage: $0 <binary>}"
out="$(mktemp)"
"$bin" -no-open -idle-exit 0 >"$out" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true; rm -f "$out"' EXIT

url=""
for _ in $(seq 1 100); do
  url="$(grep -Eo 'http://127\.0\.0\.1:[0-9]+/#[0-9a-f]+' "$out" | head -1 || true)"
  [ -n "$url" ] && break
  sleep 0.1
done
[ -n "$url" ] || { echo "FAILED: it did not print the URL"; cat "$out"; exit 1; }
base="${url%%/#*}"
token="${url##*#}"

code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
check() { # description expected got
  if [ "$2" != "$3" ]; then echo "FAILED: $1: expected $2, got $3"; exit 1; fi
  echo "ok: $1 ($3)"
}
check "API without token"   403 "$(code "$base/api/profiles")"
check "API with bad token"  403 "$(code -H 'X-Token: incorrecto' "$base/api/profiles")"
check "API with token"      200 "$(code -H "X-Token: $token" "$base/api/profiles")"
check "UI"                   200 "$(code "$base/")"
curl -sI "$base/" | tr -d '\r' | grep -qi "^content-security-policy: default-src 'self'" \
  && echo "ok: CSP present" || { echo "FAILED: CSP missing"; exit 1; }
