# ldapnav

A minimal, read-only LDAP browser. A single Go binary that serves an embedded web UI (`ui.html`, via `embed`) on 127.0.0.1 and opens the browser.

## Structure
- `main.go`: HTTP server, JSON API under `/api/*`, LDAP client (github.com/go-ldap/ldap/v3) and keychain access (github.com/zalando/go-keyring).
- `ui.html`: vanilla HTML/CSS/JS UI, no build step, no dependencies.
- `Makefile` + `Info.plist`: packaging as a macOS `.app`.

## Invariants (do not break)
- Read-only: no Add/Modify/Delete against the LDAP server unless explicitly requested.
- Listens on 127.0.0.1 only. The whole API requires the `X-Token` header; the token goes in the URL `#` fragment.
- Never store passwords in `profiles.json`: only in the system keychain (`go-keyring`), service `ldapnav`, account `bindDN@url`. Do not change the service or account: it would break already-saved passwords.
- No CDNs or external resources in the UI: the CSP blocks them on purpose.
- Must build with `CGO_ENABLED=0` for darwin/arm64, darwin/amd64 and windows/amd64.
- Self-contained binary: no Java/Node/Electron runtime and nothing downloaded at run time.
- Few dependencies, only justified ones (go-ldap, go-keyring). Test infrastructure must not add dependencies to the binary: the test LDAP server is the system `slapd`, not a Go library.

## Scope
- Browse the tree, view entry attributes and run searches. Read-only. Do not grow it into a full editor unless asked.
- go-ldap is pinned to v3.4.11 for compatibility with Go 1.24; it can be bumped with a newer Go.
- `-idle-exit` defaults to 3m on purpose: browsers throttle timers in background tabs, and a shorter value would make the app quit while the tab is open.
- Known and accepted: `hasSubordinates` does not exist in AD, so every node shows ▶.

## Tests (what they validate, and the rules)
- `main_test.go`: API (403/200 with token), UI/CSP, profiles without passwords on disk, keychain service/account format (mock), code invariants (read-only, 127.0.0.1 only, no external resources in `ui.html`) and CGO-free builds for the 3 targets.
- `integration_test.go` + `slapd_test.go` + `testdata/seed.ldif`: against a real ephemeral OpenLDAP (one shared slapd; the app is read-only). Covers TLS/StartTLS, paging (users with `limits` in slapd.conf who only see >100 results when paging) and the server size limit.
- `keyring_real_test.go`: real keychain (including an end-to-end test with a real slapd); only with `LDAPNAV_TEST_REAL_KEYRING=1` and in a separate process (`-run TestRealKeyring`), because the other tests use `keyring.MockInit`, which cannot be undone.
- slapd is a validation tool, not the code under test: it can run anywhere reachable on 127.0.0.1 (local binary, or `LDAPNAV_SLAPD_WSL=<distro>` for a Linux slapd inside WSL). What must run on each OS is the app.
- `hack/ci-test.sh` is what validates a PR; `hack/Dockerfile.test` runs it in a reproducible environment (`make test-docker`); `hack/smoke.sh <binary>` smoke-tests a built binary.
- CI (`.github/workflows/ci.yml`) has one job per OS (no conditionals) and sets `LDAPNAV_REQUIRE_SLAPD=1`: without slapd the run fails instead of skipping. Linux: test container; macOS: Homebrew openldap; Windows: Ubuntu slapd in WSL1 (macOS runners have no Docker; Windows runners only run Windows containers). Do not add tests that skip silently, and do not make a failing environment skip to turn CI green.
- Every behavior change comes with a test. `main()` only wires things up; the HTTP logic lives in `newHandler` so it can be tested with `httptest`.
- Not verified yet against real AD or production OpenLDAP servers; say explicitly what could not be tested from this environment (macOS keychain, `.app`, Windows Credential Manager).

## Docs
- `docs/USAGE.md` is the user manual, including the supported / not supported lists. Keep it in sync with behavior changes (same commit).

## Keychain notes
- `go-keyring` on macOS still calls `/usr/bin/security` internally and stores values with the `go-keyring-base64:` prefix; it reads old plain-text entries fine. Known limitation: `security -w` prints non-ASCII values hex-encoded, so legacy entries with non-ASCII passwords were already unreadable before the migration; re-saving the profile password fixes them.

## Open items / ideas
- Windows packaging (.exe with an icon, or without a console via `-ldflags -H=windowsgui`).

## Commands
- `go build -o ldapnav .` and `./ldapnav` (or `make build` / `make app` on macOS).
- Windows: `go build -o ldapnav.exe .`
- `go vet ./...` and `make test` (or `make test-docker`) before calling anything done.

## Conventions
- Everything in the repo is in English: code comments, UI text, error messages, docs, commit messages.
- Small, descriptive commits. If a decision changes the architecture or the constraints, update this file in the same commit.
