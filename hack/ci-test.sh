#!/usr/bin/env bash
# What validates a PR (runs inside hack/Dockerfile.test, or locally with slapd
# installed): formatting, go.mod, vet, tests against a real OpenLDAP and a
# smoke test of the binary.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== gofmt"
unformatted="$(gofmt -l .)"
[ -z "$unformatted" ] || { echo "Not formatted:"; echo "$unformatted"; exit 1; }

echo "== go mod tidy (go.mod/go.sum up to date)"
cp go.mod /tmp/go.mod.bak; cp go.sum /tmp/go.sum.bak
go mod tidy
cmp go.mod /tmp/go.mod.bak && cmp go.sum /tmp/go.sum.bak \
  || { echo "go.mod/go.sum were not up to date: run go mod tidy"; exit 1; }

echo "== go vet"
CGO_ENABLED=0 go vet ./...

echo "== go test (real OpenLDAP; no CGO)"
CGO_ENABLED=0 LDAPNAV_REQUIRE_SLAPD=1 go test -count=1 -v ./...

echo "== binary smoke test"
bin="$(mktemp -d)/ldapnav"
CGO_ENABLED=0 go build -o "$bin" .
hack/smoke.sh "$bin"
echo "ALL OK"
