# Roadmap: query assistant

Goal: make browsing and querying easy for people who do not know every LDAP attribute. Everything stays **read-only**, self-contained (no new runtime dependencies) and offline.

Design rule: logic lives in Go (tested against a real OpenLDAP in CI); pure JavaScript logic lives in small modules with `node --test` unit tests; every UI change has a browser test (`e2e/`).

| Phase | What | Status |
|---|---|---|
| 0 | Test foundation: real-OpenLDAP harness (`internal/slapdtest`), browser tests (`e2e/`), CI on Linux, macOS and Windows | Done |
| 1 | Server discovery: RootDSE and server flavor, schema (attribute syntaxes and matching rules, object classes), attribute usage sampling (API: `/api/server`, `/api/schema`, `/api/sample`) | API done; the UI starts using it in phase 3 |
| 2 | Results table: choose columns, sort, export CSV | Done |
| 3 | Query builder (done): rows of attribute · operator · value with AND/OR, live filter preview with correct escaping, plain-language explanation, attribute autocomplete from the schema, value inputs that fit the attribute syntax (date picker, TRUE/FALSE, DN picker) | Done |
| 4 | Recipes (done; only those the server supports, detected from the schema): find people, group membership (`member`, `uniqueMember`, `memberUid`, `memberOf` where present), account status (`nsAccountLock`, `pwdAccountLockedTime`, `shadowExpire`, timestamps), explore an unknown branch (object-class histogram and attribute usage, click to build a query) | Done |
| 5 | Saved queries per profile (a file next to `profiles.json`, never with passwords) | Done |
| 6 | Navigation: quick filter in the tree, go to a DN, jump to the entry from DN values (`member`, `manager`, `owner`), breadcrumb | Done |

Not planned: write operations, ACL inspection, SASL. Client-certificate TLS (mTLS) only if it turns out to be needed.

Target server family: OpenLDAP and 389 Directory Server first; Active Directory is expected to work for browsing but is untested.

## Also done: everyday tools for a read-only client

| What | Notes |
|---|---|
| Copy anything with one click | attribute names, values, all values, DNs, filters, table cells; confirmations as toasts |
| Exports | LDIF (RFC 2849 encoder, exact bytes) for entries and results, JSON, CSV |
| Binary and date values | photos drawn, download and base64 copy, dates in plain words |
| Groups of an entry, compare two entries | uses the schema to know how groups list their members |
| Bookmarks and recent searches | per profile, stored next to `profiles.json` |
| Server tab | server type, features with readable names, schema browser with inheritance |
| Keyboard shortcuts | `/`, `t`, `g` + letter, `?` |
| Theme | light, dark or system, remembered |
| Front-end design system | CSS layers and tokens, reusable `ui/` components, `-front-dir` for fast iteration |
| Everything is a `make` target | CI runs the same targets on Linux (amd64, arm64), macOS and Windows |

## Ideas not done yet

- Reverse "members of this group" as a table (the member DNs are already links).
- A tree-wide search box (today the tree filter only sees the nodes already opened).
- Per-profile default base DN for the Explore tab, and a configurable vault auto-lock time.
- Windows packaging (icon, no console window), signed and notarised macOS build.
