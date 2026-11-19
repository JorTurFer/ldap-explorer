#!/usr/bin/env bash
# What validates a PR (runs inside hack/Dockerfile.test, or locally with slapd
# installed): formatting, go.mod, vet, tests against a real OpenLDAP and a
# smoke test of the binary.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== gofmt"
unformatted="$(gofmt -l . e2e)"
[ -z "$unformatted" ] || { echo "Not formatted:"; echo "$unformatted"; exit 1; }

echo "== go mod tidy (go.mod/go.sum up to date)"
for m in . e2e; do
  cp "$m/go.mod" /tmp/go.mod.bak; cp "$m/go.sum" /tmp/go.sum.bak
  (cd "$m" && go mod tidy)
  cmp "$m/go.mod" /tmp/go.mod.bak && cmp "$m/go.sum" /tmp/go.sum.bak \
    || { echo "$m/go.mod or go.sum were not up to date: run go mod tidy in $m"; exit 1; }
done

echo "== go vet"
CGO_ENABLED=0 go vet ./...

echo "== go test (real OpenLDAP; no CGO)"
CGO_ENABLED=0 LDAPNAV_REQUIRE_SLAPD=1 go test -count=1 -v ./...

echo "== JS unit tests (node --test)"
LDAPNAV_REQUIRE_NODE=1 hack/jstest.sh

echo "== browser tests (real Chrome + real ldapnav + real OpenLDAP)"
(cd e2e && CGO_ENABLED=0 go vet ./... && LDAPNAV_REQUIRE_CHROME=1 LDAPNAV_REQUIRE_SLAPD=1 go test -count=1 -v ./...)

echo "== binary smoke test"
bin="$(mktemp -d)/ldapnav"
CGO_ENABLED=0 go build -o "$bin" .
hack/smoke.sh "$bin"
echo "ALL OK"
