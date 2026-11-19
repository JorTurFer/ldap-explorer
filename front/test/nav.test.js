import test from "node:test";
import assert from "node:assert/strict";
import { splitDN, ancestors, parentDN, normDN, looksLikeDN, matchesFilter } from "../js/nav.js";

test("splitDN splits on commas and semicolons and keeps positions", () => {
  const dn = "uid=alice,ou=people,dc=example,dc=org";
  assert.deepEqual(splitDN(dn).map((p) => p.rdn), ["uid=alice", "ou=people", "dc=example", "dc=org"]);
  for (const p of splitDN(dn)) assert.equal(dn.slice(p.start, p.end), p.rdn);
  assert.deepEqual(splitDN("cn=a;ou=b").map((p) => p.rdn), ["cn=a", "ou=b"]);
});

test("splitDN honours escapes", () => {
  assert.deepEqual(splitDN("cn=Smith\\, John,ou=people").map((p) => p.rdn), ["cn=Smith\\, John", "ou=people"]);
  assert.deepEqual(splitDN("cn=a\\\\,ou=b").map((p) => p.rdn), ["cn=a\\\\", "ou=b"]); // escaped backslash, then a real comma
  assert.deepEqual(splitDN("cn=a\\2Cb,ou=c").map((p) => p.rdn), ["cn=a\\2Cb", "ou=c"]); // hex escape
  assert.deepEqual(splitDN("cn=Weird (Name) *star* \\5Cback,ou=special").map((p) => p.rdn), ["cn=Weird (Name) *star* \\5Cback", "ou=special"]);
});

test("splitDN honours quotes and multi-valued RDNs", () => {
  assert.deepEqual(splitDN('cn="a,b",ou=c').map((p) => p.rdn), ['cn="a,b"', "ou=c"]);
  assert.deepEqual(splitDN("cn=a+uid=b,ou=c").map((p) => p.rdn), ["cn=a+uid=b", "ou=c"]);
});

test("splitDN drops the spaces around separators but keeps positions", () => {
  const dn = "cn=a , ou=b ,dc=c";
  const parts = splitDN(dn);
  assert.deepEqual(parts.map((p) => p.rdn), ["cn=a", "ou=b", "dc=c"]);
  for (const p of parts) assert.equal(dn.slice(p.start, p.end), p.rdn);
});

test("ancestors lists the topmost entry first and keeps the original text", () => {
  assert.deepEqual(ancestors("uid=alice,ou=people,dc=example,dc=org"),
    ["dc=org", "dc=example,dc=org", "ou=people,dc=example,dc=org", "uid=alice,ou=people,dc=example,dc=org"]);
  assert.deepEqual(ancestors("cn=Smith\\, John,ou=p"), ["ou=p", "cn=Smith\\, John,ou=p"]);
  assert.deepEqual(ancestors("dc=org"), ["dc=org"]);
  assert.deepEqual(ancestors(""), []);
  assert.deepEqual(ancestors("cn=a, ou=b"), ["ou=b", "cn=a, ou=b"]);
});

test("parentDN", () => {
  assert.equal(parentDN("uid=alice,ou=people,dc=x"), "ou=people,dc=x");
  assert.equal(parentDN("dc=x"), "");
  assert.equal(parentDN("cn=Smith\\, John,ou=p"), "ou=p");
});

test("normDN makes DNs comparable", () => {
  assert.equal(normDN("UID=Alice , OU=People,dc=Example"), "uid=alice,ou=people,dc=example");
  assert.equal(normDN("cn = a + sn = b,dc=x"), "cn=a+sn=b,dc=x");
  assert.equal(normDN("dc=x"), normDN(" DC=X "));
  assert.notEqual(normDN("cn=a,dc=x"), normDN("cn=b,dc=x"));
});

test("looksLikeDN tells a DN from a search word", () => {
  assert.equal(looksLikeDN("uid=alice,ou=people,dc=example,dc=org"), true);
  assert.equal(looksLikeDN("dc=org"), true);
  assert.equal(looksLikeDN("alice"), false);
  assert.equal(looksLikeDN("people"), false);
  assert.equal(looksLikeDN(""), false);
});

test("matchesFilter: all words, any order, case-insensitive", () => {
  const dn = "uid=Alice,ou=People,dc=example,dc=org";
  assert.equal(matchesFilter(dn, "alice"), true);
  assert.equal(matchesFilter(dn, "PEOPLE alice"), true);
  assert.equal(matchesFilter(dn, "alice bob"), false);
  assert.equal(matchesFilter(dn, ""), true);
  assert.equal(matchesFilter(dn, "   "), true);
  assert.equal(matchesFilter(dn, "ou=people"), true);
});
