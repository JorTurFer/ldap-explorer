# ldap-explorer

A minimal, read-only LDAP browser. A single Go binary that serves an embedded web UI (`front/`, via `embed`) on 127.0.0.1 and opens the browser.

## Structure
- `main.go`: HTTP server, JSON API under `/api/*`, LDAP client (github.com/go-ldap/ldap/v3) and keychain access (github.com/zalando/go-keyring).
- Credentials: `credentials.go` (stores, `/api/vault/*`, `/api/credentials*`) and `internal/vault` (Argon2id + AES-256-GCM per entry bound to its HMAC identifier so the file reveals no accounts, wrong-passphrase check, throttled unlocks, wipe on lock, idle auto-lock, atomic 0600 writes, bounds on the stored KDF cost). Changing the file format needs a version bump and a migration; keep reading old versions. A locked vault answers `423 vault_locked` and the UI unlocks and retries.
- Query assistant (all read-only; roadmap and status in `docs/ROADMAP.md`):
  - `discovery.go` + `internal/ldapschema`: RootDSE and server flavor, parsed schema (attribute syntaxes, matching rules, object classes), attribute usage sampling. The schema is cached per connection in `sess` together with name indexes.
  - `filterapi.go` + `internal/ldapfilter`: `POST /api/filter` builds, escapes, validates against the schema and explains an LDAP filter from a tree of conditions. It is the ONLY place filters are built from user input: never concatenate user text into a filter anywhere else.
  - `recipesapi.go` + `internal/recipes`: ready-made queries built from what the server's schema defines; unavailable ones carry the reason.
  - `queries.go`: saved queries per profile in `queries.json` (atomic write, 0600, never credentials, a corrupt file is reported and never overwritten).
- `front/`: the web UI, plain HTML + CSS + native ES modules. No build step, no bundler, no framework, no npm dependencies. No authentication in the UI (the URL-fragment token is the only gate).
  - `index.html`, `css/style.css`, `js/*.js` (one module per concern: `api`, `tree`, `entry`, `search`, `results`, `builder`, `recipes`, `explore`, `savedqueries`, `schema`, `profiles`, `connection`, `quick` (Connect as…), `quickfind` (the header search box), `vault` (create/unlock dialogs and the Passwords panel), `server` (Server tab and schema browser), `bookmarks`, `history`, `compare`, `shortcuts`, `settings`, `tabs`, `status`; shared pieces in `ui/` (`copy.js`, `toast.js`, `download.js`); styles in `css/` split into `tokens`, `base`, `layout`, `components` (edit tokens to change the look); pure logic with no DOM access and node tests: `table.js` (columns, sorting, CSV), `builder-model.js` (operators per attribute type, conversion to the API query), `nav.js` (DN splitting honouring escapes, ancestors, tree filter matching), `cred-model.js` (storage options and hints, quick-switch labels, passphrase checks), `labels.js` (friendly attribute names, quick-add chips, options summary), `decode.js` (GeneralizedTime, relative time, sizes), `diff.js` (entry comparison), `schema-model.js` (schema filtering, inheritance, readable control names), `history-model.js`, `shortcuts-model.js` (key sequences), `settings-model.js`; `app.js` wires them; modules talk through the tiny bus in `events.js`, not by importing each other in circles).
  - Strict CSP (`script-src 'self'; style-src 'self'`): no inline `<script>`, `<style>`, `style=` attributes or `on*=` handlers, no `setAttribute("style")`, no `innerHTML`, no `eval`. Use CSS classes. Tests enforce it.
  - Only `front/index.html`, `front/css` and `front/js` are embedded; `front/test` (Node unit tests) is not.
- Read-only tools for the people using it: `export.go` + `internal/ldif` (LDIF encoder/decoder, exact bytes; `/api/entry/ldif`, `/api/search/ldif`), `value.go` (`/api/entry/value`, the exact bytes of one value), `groups.go` (`/api/entry/groups`, uses the recipe logic), `userdata.go` (bookmarks and search history per profile), `settings.go` (theme), `queries.go` (saved queries). Every file written by the app goes through `writePrivateJSON` (atomic, 0600) and a damaged one is reported, never overwritten.
- `Makefile` (the single entry point, see Commands) + `Info.plist`: packaging as a macOS `.app`.

## Invariants (do not break)
- Read-only: no Add/Modify/Delete against the LDAP server unless explicitly requested.
- Listens on 127.0.0.1 only. The whole API requires the `X-Token` header; the token goes in the URL `#` fragment.
- Never store passwords in `profiles.json`, never return one from the API, never log request bodies. A profile's password lives in the system keychain (`go-keyring`; service `ldap-explorer`, account `bindDN@url`) or in the encrypted vault (`internal/vault`, same account string), as chosen in `Profile.Storage` (`none`, `keychain`, `vault`; the old `useKeychain` is read as keychain). Changing where it lives moves it; changing the user or deleting the profile forgets the old copy; a save that cannot store the password is refused before anything changes. Do not change the service or account format: it would break already-saved passwords. The previous service name `ldapnav` is still read (legacyKeychainService) and copied to the new one; never delete the old entries.
- No CDNs or external resources in the UI: the CSP blocks them on purpose.
- Must build with `CGO_ENABLED=0` for darwin/arm64, darwin/amd64 and windows/amd64.
- Self-contained binary: no Java/Node/Electron runtime and nothing downloaded at run time.
- Few dependencies, only justified ones (go-ldap, go-keyring). Test infrastructure must not add dependencies to the binary: the test LDAP server is the system `slapd`, not a Go library.

## Scope
- Browse the tree, view entry attributes and run searches. Read-only. Do not grow it into a full editor unless asked.
- The toolchain is Go 1.27.4 (`go.mod`, `hack/Dockerfile.test`; CI reads the version from `go.mod`). go-ldap is still pinned to v3.4.11 from the time the project used Go 1.24; the reason is gone, so it can be bumped.
- `-idle-exit` defaults to 3m on purpose: browsers throttle timers in background tabs, and a shorter value would make the app quit while the tab is open.
- Known and accepted: `hasSubordinates` does not exist in AD, so every node shows ▶.

## Tests (what they validate, and the rules)
- `main_test.go`: API (403/200 with token), UI static serving and strict CSP, no inline code or external resources in `front/`, every import resolves, profiles without passwords on disk, keychain service/account format (mock), code invariants (read-only, 127.0.0.1 only) and CGO-free builds for the 3 targets.
- `integration_test.go` + `internal/slapdtest` (harness and `seed.ldif`, importable by other modules): against a real ephemeral OpenLDAP (one shared slapd; the app is read-only). Covers TLS/StartTLS, paging (users with `limits` in slapd.conf who only see >100 results when paging) and the server size limit.
- `front/test/*.test.js` + `hack/jstest.sh`: unit tests of the pure JS modules with `node --test` (no npm dependencies, Node is dev tooling only; `front/package.json` only marks the modules as ESM for Node). `LDAP_EXPLORER_REQUIRE_NODE=1` makes a missing Node fail instead of skip. Logic that needs no DOM goes in a pure module like `table.js`, not in the DOM modules.
- `e2e/` (separate Go module, uses chromedp): builds the real binary and drives its UI in headless Chrome against a real slapd (`internal/slapdtest`). Fails on any JS exception, console error or CSP violation (HTTP 400 from `/api/` is expected). Any UI change comes with an e2e test. `LDAP_EXPLORER_REQUIRE_CHROME=1` makes a missing Chrome fail instead of skip. chromedp is pinned to v0.14.2 on purpose (v0.24 changed its API to generics); keep that dependency out of the root `go.mod`.
- `keyring_real_test.go`: real keychain (including an end-to-end test with a real slapd); only with `LDAP_EXPLORER_TEST_REAL_KEYRING=1` and in a separate process (`-run TestRealKeyring`), because the other tests use `keyring.MockInit`, which cannot be undone.
- slapd is a validation tool, not the code under test: it can run anywhere reachable on 127.0.0.1 (local binary, or `LDAP_EXPLORER_SLAPD_WSL=<distro>` for a Linux slapd inside WSL). What must run on each OS is the app.
- `hack/ci-test.sh` is what validates a PR; `hack/Dockerfile.test` runs it in a reproducible environment (`make test-docker`); `hack/smoke.sh <binary>` smoke-tests a built binary.
- CI (`.github/workflows/ci.yml`) has one job per OS (no conditionals) and sets `LDAP_EXPLORER_REQUIRE_SLAPD=1`: without slapd the run fails instead of skipping. Linux: test container; macOS: Homebrew openldap; Windows: Ubuntu slapd in WSL1 (macOS runners have no Docker; Windows runners only run Windows containers). Do not add tests that skip silently, and do not make a failing environment skip to turn CI green.
- Pure JavaScript logic that grows in the UI (value formatting, CSV export, table sorting) goes in its own module with unit tests run by `node --test` (no npm dependencies; Node is dev tooling only, never shipped).
- A layout bug is a bug: check that controls are not clipped (the builder test asserts it) and regenerate the screenshots after visible UI changes; they already exposed one clipped row.
- Copy affordances: anything that shows a value a person might need elsewhere gets a `copy` button (`ui/copy.js`) with a confirmation; new UI follows that.
- Every behavior change comes with a test. `main()` only wires things up; the HTTP logic lives in `newHandler` so it can be tested with `httptest`.
- Real servers differ: the account recipes (`nsAccountLock`, `passwordExpirationTime`, ppolicy) are only checked against synthetic schemas, and OpenLDAP's `shadowExpire` has no ordering rule (the builder refuses `<`/`>` on it). Not verified yet against real AD or production OpenLDAP servers; say explicitly what could not be tested from this environment (macOS keychain, `.app`, Windows Credential Manager).

## Docs
- `docs/FRONTEND.md` explains how the front end is organised and how to iterate on it (rules, tokens, adding a tab or a feature). Keep it true when the structure changes.
- Screenshots in `docs/img` are generated from the real UI by `e2e/screenshots_test.go` (`make screenshots`, needs Docker). Regenerate them in the same commit as any visible UI change; never edit them by hand.
- `docs/USAGE.md` is the user manual, including the supported / not supported lists. Keep it in sync with behavior changes (same commit).

## Keychain notes
- `go-keyring` on macOS still calls `/usr/bin/security` internally and stores values with the `go-keyring-base64:` prefix; it reads old plain-text entries fine. Known limitation: `security -w` prints non-ASCII values hex-encoded, so legacy entries with non-ASCII passwords were already unreadable before the migration; re-saving the profile password fixes them.

## Open items / ideas
- Windows packaging (.exe with an icon, or without a console via `-ldflags -H=windowsgui`).

## Commands
- Everything is a `make` target and CI runs the same ones (`make help`): `make ci` (lint + all tests + smoke), `make test-docker` (that, reproducibly, in a container), `make test-keychain` (real OS keychain), `make build-all`, `make screenshots`, `make front-dev` (serve `front/` from disk with `-front-dir`: edit and refresh, no rebuild). Add a Makefile target for anything new that is run by hand or by CI; do not put logic only in a workflow.
- Before calling anything done run `make ci` (or `make test-docker` when slapd, Chrome or Node are not installed locally).

## Conventions
- Everything in the repo is in English: code comments, UI text, error messages, docs, commit messages.
- Small, descriptive commits. If a decision changes the architecture or the constraints, update this file in the same commit.
