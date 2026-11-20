import test from "node:test";
import assert from "node:assert/strict";
import { PAGE_SIZE, childrenURL, loadedText } from "../js/tree-model.js";

test("the first page asks for a page size, the next ones send the cursor", () => {
  assert.equal(childrenURL("ou=a,dc=x"), `/api/children?dn=ou%3Da%2Cdc%3Dx&size=${PAGE_SIZE}`);
  assert.equal(childrenURL("ou=a,dc=x", "abc123"), "/api/children?dn=ou%3Da%2Cdc%3Dx&cursor=abc123");
  assert.equal(childrenURL("cn=a\\,b", "x y"), "/api/children?dn=cn%3Da%5C%2Cb&cursor=x%20y");
});

test("loadedText", () => {
  assert.equal(loadedText(1200), "1,200 loaded, more below");
});
