import test from "node:test";
import assert from "node:assert/strict";
import { diffEntries, summarize, summaryText, isIdentical } from "../js/diff.js";

const alice = { dn: "uid=alice,dc=x", attrs: [
  { name: "uid", values: ["alice"] }, { name: "objectClass", values: ["inetOrgPerson", "top"] },
  { name: "mail", values: ["alice@x.org"] }, { name: "jpegPhoto", values: ["(binary, 676 bytes)"] },
  { name: "member", values: ["a", "b"] },
] };
const bob = { dn: "uid=bob,dc=x", attrs: [
  { name: "uid", values: ["bob"] }, { name: "OBJECTCLASS", values: ["top", "inetOrgPerson"] }, // other order and case of the name
  { name: "mail", values: ["bob@x.org"] }, { name: "telephoneNumber", values: ["1", "2"] },
  { name: "member", values: ["b", "c"] },
] };

test("statuses: same, different, only on one side", () => {
  const rows = diffEntries(alice, bob);
  const by = Object.fromEntries(rows.map((r) => [r.name.toLowerCase(), r]));
  assert.equal(by.objectclass.status, "same");       // same set of values, whatever the order
  assert.equal(by.uid.status, "different");
  assert.equal(by.mail.status, "different");
  assert.equal(by.jpegphoto.status, "onlyA");
  assert.equal(by.telephonenumber.status, "onlyB");
  assert.equal(by.member.status, "different");
});

test("which values were removed and added", () => {
  const member = diffEntries(alice, bob).find((r) => r.name === "member");
  assert.deepEqual(member.removed, ["a"]);
  assert.deepEqual(member.added, ["c"]);
  assert.deepEqual(member.a, ["a", "b"]);
  assert.deepEqual(member.b, ["b", "c"]);
});

test("objectClass first, then alphabetical; the name keeps the spelling of the first entry that has it", () => {
  const rows = diffEntries(alice, bob);
  assert.deepEqual(rows.map((r) => r.name), ["objectClass", "jpegPhoto", "mail", "member", "telephoneNumber", "uid"]);
});

test("values are compared exactly: case matters", () => {
  const rows = diffEntries({ dn: "a", attrs: [{ name: "cn", values: ["Alice"] }] }, { dn: "b", attrs: [{ name: "cn", values: ["alice"] }] });
  assert.equal(rows[0].status, "different");
});

test("summary counts and text", () => {
  const s = summarize(diffEntries(alice, bob));
  assert.deepEqual(s, { same: 1, different: 3, onlyA: 1, onlyB: 1 });
  assert.equal(summaryText(s, "alice", "bob"), "3 differ, 1 only in alice, 1 only in bob, 1 identical");
  assert.equal(summaryText({ same: 0, different: 0, onlyA: 0, onlyB: 0 }), "Nothing to compare");
  assert.equal(summaryText({ same: 4, different: 0, onlyA: 0, onlyB: 0 }), "4 identical");
});

test("identical entries, and an entry against itself", () => {
  assert.equal(isIdentical(diffEntries(alice, alice)), true);
  assert.equal(isIdentical(diffEntries(alice, bob)), false);
  assert.equal(isIdentical(diffEntries({ dn: "a" }, { dn: "b" })), true); // two empty entries
});

test("the input is not modified", () => {
  const copy = JSON.stringify([alice, bob]);
  diffEntries(alice, bob);
  assert.equal(JSON.stringify([alice, bob]), copy);
});
