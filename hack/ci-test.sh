#!/usr/bin/env bash
# Lo que valida un PR (se ejecuta dentro de hack/Dockerfile.test o en local con
# slapd instalado): formato, go.mod, vet, tests con OpenLDAP real y smoke del binario.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== gofmt"
unformatted="$(gofmt -l .)"
[ -z "$unformatted" ] || { echo "Sin formatear:"; echo "$unformatted"; exit 1; }

echo "== go mod tidy (go.mod/go.sum al día)"
cp go.mod /tmp/go.mod.bak; cp go.sum /tmp/go.sum.bak
go mod tidy
cmp go.mod /tmp/go.mod.bak && cmp go.sum /tmp/go.sum.bak \
  || { echo "go.mod/go.sum no estaban al día: ejecuta go mod tidy"; exit 1; }

echo "== go vet"
CGO_ENABLED=0 go vet ./...

echo "== go test (OpenLDAP real; sin CGO)"
CGO_ENABLED=0 LDAPNAV_REQUIRE_SLAPD=1 go test -count=1 -v ./...

echo "== smoke del binario"
bin="$(mktemp -d)/ldapnav"
CGO_ENABLED=0 go build -o "$bin" .
hack/smoke.sh "$bin"
echo "TODO OK"
