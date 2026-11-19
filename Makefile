APP := LDAPExplorer.app

.PHONY: build app clean test test-docker smoke screenshots

build:
	go mod tidy
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ldap-explorer .

# Package as an .app to launch it from Finder/Spotlight/Dock.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp ldap-explorer $(APP)/Contents/MacOS/ldap-explorer
	cp Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)
	@echo "Done: drag $(APP) to /Applications"

clean:
	rm -rf ldap-explorer $(APP)

# Native tests. Integration tests use slapd (OpenLDAP) and the browser tests use
# Chrome; both are skipped if missing, unless LDAP_EXPLORER_REQUIRE_SLAPD=1 /
# LDAP_EXPLORER_REQUIRE_CHROME=1 (then they fail instead of skipping).
test:
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go test -count=1 ./...
	hack/jstest.sh
	cd e2e && CGO_ENABLED=0 go vet ./... && go test -count=1 ./...

# Reproducible environment (Go + OpenLDAP in a container): same as what CI runs.
test-docker:
	docker build -f hack/Dockerfile.test -t ldap-explorer-test .
	docker run --rm ldap-explorer-test

# Smoke test of the built binary (403 without a token, 200 with one).
smoke: build
	hack/smoke.sh ./ldap-explorer

# Regenerate the screenshots in docs/img from the real UI. Needs Docker: the
# images are rendered inside the test image so they look the same everywhere.
screenshots:
	docker build -f hack/Dockerfile.test -t ldap-explorer-test .
	docker run --rm -v "$(CURDIR)/docs/img:/out" -e LDAP_EXPLORER_SCREENSHOTS=/out ldap-explorer-test \
	  bash -c 'cd e2e && go test -count=1 -run TestScreenshots ./...'
