APP := LDAPNav.app

.PHONY: build app clean test test-docker smoke

build:
	go mod tidy
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ldapnav .

# Empaqueta como .app para lanzarlo desde Finder/Spotlight/Dock.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp ldapnav $(APP)/Contents/MacOS/ldapnav
	cp Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)
	@echo "Listo: arrastra $(APP) a /Applications"

clean:
	rm -rf ldapnav $(APP)

# Tests nativos. Los de integración usan slapd (OpenLDAP) si está instalado y,
# si no, se saltan; con LDAPNAV_REQUIRE_SLAPD=1 fallan en lugar de saltarse.
test:
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go test -count=1 ./...

# Entorno reproducible (Go + OpenLDAP en contenedor): lo mismo que ejecuta la CI.
test-docker:
	docker build -f hack/Dockerfile.test -t ldapnav-test .
	docker run --rm ldapnav-test

# Smoke test del binario compilado (403 sin token, 200 con token).
smoke: build
	hack/smoke.sh ./ldapnav
