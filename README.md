# ldapnav

A minimal, read-only LDAP browser for macOS and Windows. A single Go binary that serves the UI on `127.0.0.1` and opens your browser.

- Browsable tree (double-click or ▶ to expand, click to view attributes)
- Searches with base, filter, scope, attributes and limit (paged)
- Optional operational attributes; binary values are shown as a hex summary
- Profiles saved in `~/Library/Application Support/ldapnav/profiles.json` on macOS (no password); the equivalent user config directory on other systems
- Optional password in the system keychain (Keychain on macOS, Credential Manager on Windows, Secret Service on Linux) via `go-keyring`; service `ldapnav`, account `bindDN@url`
- `ldap://`, `ldaps://` and StartTLS; base DN auto-detected from the RootDSE

## Build

Requires Go ≥ 1.24. No CGO needed.

```sh
make build        # ./ldapnav binary
./ldapnav         # opens the browser

make app          # LDAPNav.app (ad-hoc signed), drag it to /Applications
```

On Windows: `go build -o ldapnav.exe .`

When you build it on your own machine the binary has no quarantine attribute, so Gatekeeper does not complain.

## Options

```
-port 0            local port (0 = random)
-no-open           do not open the browser
-idle-exit 3m      exit if the tab has been closed for this long (0 = never)
```

## Security

- Listens only on `127.0.0.1`.
- Every start generates a random token that goes in the URL `#` fragment. The API requires it in a header, so other web pages open in the browser cannot use the server.
- Nothing is written to the LDAP server: it only does bind and search.

## Tests

```sh
make test          # native go vet + go test; integration tests use slapd if installed
make test-docker   # Go + OpenLDAP in a container (hack/Dockerfile.test): reproducible, same as CI
make smoke         # build and check the binary (403 without a token, 200 with one)
```

The integration tests start an ephemeral `slapd` (OpenLDAP) loaded with `testdata/seed.ldif`
plus 600 generated entries, on `ldap://` and `ldaps://` (self-signed certificate), and exercise the real API:
good/bad/anonymous bind, TLS and StartTLS, tree, entry view, searches, paging and server limits.
`slapd` is looked up in `LDAPNAV_SLAPD_DIR`, in `PATH` and in common locations (apt, brew…); if it is not found
the tests are skipped, unless `LDAPNAV_REQUIRE_SLAPD=1` (as CI sets it). To install it: `apt install slapd ldap-utils`
(Debian/Ubuntu) or `brew install openldap` (macOS).

The real-keychain tests (Keychain / Credential Manager) only run with
`LDAPNAV_TEST_REAL_KEYRING=1 go test -run TestRealKeyring ./...`; they use temporary accounts under the `ldapnav` service and delete them afterwards.

### CI

`.github/workflows/ci.yml` (on every PR and push to `main`):
- **linux**: `hack/Dockerfile.test` → gofmt, `go mod tidy` with no changes, `go vet`, tests against a real OpenLDAP and a binary smoke test.
- **macos / windows**: real keychain, tests without LDAP, build + binary smoke test and, on macOS, `make app` + `codesign --verify` + `.app` smoke test.
