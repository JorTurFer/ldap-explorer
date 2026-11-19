# Everything you can do with this repository is a target here, and CI runs these
# same targets, so what passes on your machine is what passes in CI.
# `make help` lists them.

SHELL := bash
.DEFAULT_GOAL := help

NAME  := ldap-explorer
APP   := LDAPExplorer.app
DIST  := dist
IMAGE := ldap-explorer-test
export CGO_ENABLED := 0 # the app must build and test without cgo

ifeq ($(OS),Windows_NT)
BIN := $(NAME).exe
else
BIN := $(NAME)
endif

# Targets built by `make build-all`, as os/arch.
PLATFORMS := darwin/arm64 darwin/amd64 windows/amd64 linux/amd64 linux/arm64

# -v for verbose test output: make test V=-v
V ?=

.PHONY: help build build-all app clean \
        fmt fmt-check vet tidy tidy-check lint-js lint \
        test test-go test-js test-e2e test-keychain smoke ci \
        test-docker screenshots front-dev

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---------- build ----------

build: ## Build ./ldap-explorer (ldap-explorer.exe on Windows)
	go build -trimpath -ldflags="-s -w" -o $(BIN) .

build-all: ## Cross-compile for macOS (arm64, amd64), Windows, Linux (amd64, arm64) into dist/
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  echo "building $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="-s -w" -o $(DIST)/$(NAME)-$$os-$$arch$$ext .; \
	done

app: build ## Package LDAPExplorer.app (macOS), ad-hoc signed: drag it to /Applications
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp $(BIN) $(APP)/Contents/MacOS/$(NAME)
	cp Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)
	@echo "Done: drag $(APP) to /Applications"

clean: ## Remove the build output (the binary, the .app and dist/)
	rm -rf $(BIN) $(APP) $(DIST)

# ---------- static checks ----------

fmt: ## Format the Go code
	gofmt -w . e2e

fmt-check: ## Fail if any Go file is not gofmt-formatted
	@out="$$(gofmt -l . e2e)"; if [ -n "$$out" ]; then echo "Not formatted:"; echo "$$out"; exit 1; fi

vet: ## go vet (the app and the browser tests)
	go vet ./...
	cd e2e && go vet ./...

tidy: ## go mod tidy in both modules
	go mod tidy
	cd e2e && go mod tidy

tidy-check: ## Fail if go.mod or go.sum are not tidy
	go mod tidy -diff
	cd e2e && go mod tidy -diff

lint-js: ## Syntax-check every JavaScript file (needs Node)
	hack/jslint.sh

lint: fmt-check vet tidy-check lint-js ## All the static checks

# ---------- tests ----------
# They need slapd (OpenLDAP) and Chrome and skip what is missing, unless
# LDAP_EXPLORER_REQUIRE_SLAPD=1 / _CHROME=1 / _NODE=1 (CI sets them: then they fail).

test-go: ## Go tests: unit tests and integration tests against a real OpenLDAP
	go test -count=1 $(V) ./...

test-js: ## Unit tests of the front-end logic (node --test)
	hack/jstest.sh

test-e2e: ## Browser tests: real Chrome, real binary, real OpenLDAP
	cd e2e && go test -count=1 $(V) ./...

test-keychain: ## Tests against the REAL OS keychain (macOS Keychain / Windows Credential Manager)
	LDAP_EXPLORER_TEST_REAL_KEYRING=1 go test -count=1 -v -run TestRealKeyring ./...

test: test-go test-js test-e2e ## All the tests

smoke: build ## Start the built binary and check 403 without a token, 200 with one
	hack/smoke.sh ./$(BIN)

ci: lint test smoke ## Everything CI runs on every platform

# ---------- reproducible environment, docs, development ----------

test-docker: ## Run `make ci` in a container with Go, OpenLDAP, Chromium and Node
	docker build -f hack/Dockerfile.test -t $(IMAGE) .
	docker run --rm $(IMAGE)

screenshots: ## Regenerate docs/img from the real UI (needs Docker)
	docker build -f hack/Dockerfile.test -t $(IMAGE) .
	mkdir -p docs/img
	docker run --rm -v "$(CURDIR)/docs/img:/out" -e LDAP_EXPLORER_SCREENSHOTS=/out $(IMAGE) \
	  bash -c 'cd e2e && go test -count=1 -run TestScreenshots ./...'

front-dev: ## Run serving the UI from front/ on disk: edit, refresh the browser, no rebuild
	go run . -front-dir front
