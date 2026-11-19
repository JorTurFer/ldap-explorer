#!/usr/bin/env bash
# Smoke test de un binario ya compilado: arranca con -no-open, comprueba que la
# API devuelve 403 sin token (o con uno incorrecto) y 200 con X-Token, y que la
# UI se sirve con CSP. Uso: hack/smoke.sh ./ldapnav   (o ldapnav.exe, o el del .app)
set -euo pipefail
bin="${1:?uso: $0 <binario>}"
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
[ -n "$url" ] || { echo "FALLO: no imprimió la URL"; cat "$out"; exit 1; }
base="${url%%/#*}"
token="${url##*#}"

code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
check() { # descripción esperado obtenido
  if [ "$2" != "$3" ]; then echo "FALLO: $1: esperaba $2, obtuve $3"; exit 1; fi
  echo "ok: $1 ($3)"
}
check "API sin token"        403 "$(code "$base/api/profiles")"
check "API con token malo"   403 "$(code -H 'X-Token: incorrecto' "$base/api/profiles")"
check "API con token"        200 "$(code -H "X-Token: $token" "$base/api/profiles")"
check "UI"                   200 "$(code "$base/")"
curl -sI "$base/" | tr -d '\r' | grep -qi "^content-security-policy: default-src 'self'" \
  && echo "ok: CSP presente" || { echo "FALLO: falta la CSP"; exit 1; }
