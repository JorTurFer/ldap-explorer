import test from "node:test";
import assert from "node:assert/strict";
import { operationalHint, splitAttrs, isDNValue, OPERATIONAL_HINTS } from "../js/entry-model.js";

const attrs = (...names) => names.map((name) => ({ name, values: ["x"] }));

test("operationalHint is case-insensitive and empty for unknown names", () => {
  assert.match(operationalHint("entryUUID"), /unique id/i);
  assert.match(operationalHint("MODIFYTIMESTAMP"), /last changed/i);
  assert.equal(operationalHint("mail"), "");
  assert.equal(operationalHint(""), "");
});

test("splitAttrs trusts the schema and keeps the order of each group", () => {
  const op = new Set(["entryUUID", "createTimestamp"]);
  const r = splitAttrs(attrs("cn", "entryUUID", "mail", "createTimestamp"), (n) => op.has(n));
  assert.deepEqual(r.user.map((a) => a.name), ["cn", "mail"]);
  assert.deepEqual(r.operational.map((a) => a.name), ["entryUUID", "createTimestamp"]);
});

test("splitAttrs falls back to the known names when the schema says nothing", () => {
  const r = splitAttrs(attrs("cn", "entryUUID", "modifyTimestamp", "whenCreated", "memberOf"));
  assert.deepEqual(r.operational.map((a) => a.name), ["entryUUID", "modifyTimestamp"]);
  assert.deepEqual(r.user.map((a) => a.name), ["cn", "whenCreated", "memberOf"]); // ordinary in AD / 389
});

test("a schema that says an attribute is ordinary wins over the known names", () => {
  const r = splitAttrs(attrs("entryUUID"), () => false);
  assert.equal(r.operational.length, 0);
});

test("every hint is a sentence", () => {
  for (const [k, v] of Object.entries(OPERATIONAL_HINTS)) { assert.equal(k, k.toLowerCase()); assert.match(v, /\.$/); }
});

test("isDNValue: the schema decides when it knows the attribute", () => {
  assert.equal(isDNValue("whatever", "uid=a,dc=x", "dn"), true);
  assert.equal(isDNValue("member", "uid=a,dc=x", "text"), false);
});

test("isDNValue without a schema: only well-known attributes and values that look like a DN", () => {
  assert.equal(isDNValue("member", "uid=a,dc=x", undefined), true);
  assert.equal(isDNValue("seeAlso", "cn=b,dc=x", null), true);
  assert.equal(isDNValue("description", "cn=b,dc=x", undefined), false);
  assert.equal(isDNValue("member", "not a dn", undefined), false);
});
