APP := LDAPNav.app

.PHONY: build app clean test test-docker smoke

build:
	go mod tidy
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ldapnav .

# Package as an .app to launch it from Finder/Spotlight/Dock.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp ldapnav $(APP)/Contents/MacOS/ldapnav
	cp Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)
	@echo "Done: drag $(APP) to /Applications"

clean:
	rm -rf ldapnav $(APP)

# Native tests. Integration tests use slapd (OpenLDAP) and the browser tests use
# Chrome; both are skipped if missing, unless LDAPNAV_REQUIRE_SLAPD=1 /
# LDAPNAV_REQUIRE_CHROME=1 (then they fail instead of skipping).
test:
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go test -count=1 ./...
	hack/jstest.sh
	cd e2e && CGO_ENABLED=0 go vet ./... && go test -count=1 ./...

# Reproducible environment (Go + OpenLDAP in a container): same as what CI runs.
test-docker:
	docker build -f hack/Dockerfile.test -t ldapnav-test .
	docker run --rm ldapnav-test

# Smoke test of the built binary (403 without a token, 200 with one).
smoke: build
	hack/smoke.sh ./ldapnav
