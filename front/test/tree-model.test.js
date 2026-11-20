import test from "node:test";
import assert from "node:assert/strict";
import { PAGE_SIZE, childrenURL, loadedText, isTimeout } from "../js/tree-model.js";

test("the first page asks for a page size, the next ones send the cursor", () => {
  assert.equal(childrenURL("ou=a,dc=x"), `/api/children?dn=ou%3Da%2Cdc%3Dx&size=${PAGE_SIZE}`);
  assert.equal(childrenURL("ou=a,dc=x", "abc123"), "/api/children?dn=ou%3Da%2Cdc%3Dx&cursor=abc123");
  assert.equal(childrenURL("cn=a\\,b", "x y"), "/api/children?dn=cn%3Da%5C%2Cb&cursor=x%20y");
});

test("loadedText", () => {
  assert.equal(loadedText(1200), "1,200 loaded, more below");
});

test("a light listing asks for the names only, a cursor never changes the mode", () => {
  assert.equal(childrenURL("ou=a", "", true), `/api/children?dn=ou%3Da&size=${PAGE_SIZE}&light=1`);
  assert.equal(childrenURL("ou=a", "", false), `/api/children?dn=ou%3Da&size=${PAGE_SIZE}`);
  assert.equal(childrenURL("ou=a", "c1", true), "/api/children?dn=ou%3Da&cursor=c1"); // the server remembers it in the cursor
});

test("isTimeout recognises the ways a directory runs out of time", () => {
  for (const m of ['LDAP Result Code 201 "Network Error": ldap: connection timed out', 'LDAP Result Code 3 "Time Limit Exceeded"', "request timeout", "i/o timeout"]) {
    assert.equal(isTimeout(m), true, m);
  }
  for (const m of ['LDAP Result Code 32 "No Such Object"', "Cancelled", "", undefined]) assert.equal(isTimeout(m), false, String(m));
});
