# Roadmap: query assistant

Goal: make browsing and querying easy for people who do not know every LDAP attribute. Everything stays **read-only**, self-contained (no new runtime dependencies) and offline.

Design rule: logic lives in Go (tested against a real OpenLDAP in CI); pure JavaScript logic lives in small modules with `node --test` unit tests; every UI change has a browser test (`e2e/`).

| Phase | What | Status |
|---|---|---|
| 0 | Test foundation: real-OpenLDAP harness (`internal/slapdtest`), browser tests (`e2e/`), CI on Linux, macOS and Windows | Done |
| 1 | Server discovery: RootDSE and server flavor, schema (attribute syntaxes and matching rules, object classes), attribute usage sampling (API: `/api/server`, `/api/schema`, `/api/sample`) | API done; the UI starts using it in phase 3 |
| 2 | Results table: choose columns, sort, export CSV | Done |
| 3 | Query builder: rows of attribute · operator · value with AND/OR, live filter preview with correct escaping, plain-language explanation, attribute autocomplete from the schema, value inputs that fit the attribute syntax (date picker, TRUE/FALSE, DN picker) | Planned |
| 4 | Recipes (only those the server supports, detected from the schema): find people, group membership (`member`, `uniqueMember`, `memberUid`, `memberOf` where present), account status (`nsAccountLock`, `pwdAccountLockedTime`, `shadowExpire`, timestamps), explore an unknown branch (object-class histogram and attribute usage, click to build a query) | Planned |
| 5 | Saved queries per profile (a file next to `profiles.json`, never with passwords) | Planned |
| 6 | Navigation: quick filter in the tree, jump to the entry from DN values (`member`, `manager`, `owner`), breadcrumb | Planned |

Not planned: write operations, ACL inspection, SASL. Client-certificate TLS (mTLS) only if it turns out to be needed.

Target server family: OpenLDAP and 389 Directory Server first; Active Directory is expected to work for browsing but is untested.
