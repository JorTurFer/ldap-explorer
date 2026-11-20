import test from "node:test";
import assert from "node:assert/strict";
import { friendlyLabel, labelWithName, commonChips, chipDefaults, optionsSummary } from "../js/labels.js";

test("common attributes have friendly names, in any letter case", () => {
  assert.equal(friendlyLabel("mail"), "Email");
  assert.equal(friendlyLabel("MAIL"), "Email");
  assert.equal(friendlyLabel("  createTimestamp "), "Created");
  assert.equal(friendlyLabel("objectClass"), "Type of entry");
  assert.equal(friendlyLabel("sAMAccountName"), "Windows login");
  assert.equal(friendlyLabel("someCustomAttribute"), "");
  assert.equal(friendlyLabel(""), "");
  assert.equal(friendlyLabel(undefined), "");
});

test("labelWithName shows both when it can", () => {
  assert.equal(labelWithName("mail"), "Email (mail)");
  assert.equal(labelWithName("custom"), "custom");
});

const openldap = new Set(["cn", "sn", "givenName", "uid", "mail", "telephoneNumber", "departmentNumber", "objectClass", "createTimestamp", "modifyTimestamp"].map((s) => s.toLowerCase()));
const ad = new Set(["cn", "displayName", "sAMAccountName", "mail", "sn", "givenName", "department", "objectClass", "whenCreated", "whenChanged", "memberOf"].map((s) => s.toLowerCase()));
const has = (set) => (n) => set.has(n.toLowerCase());

test("chips only offer what the server has, one attribute per group, in a useful order", () => {
  assert.deepEqual(commonChips(has(openldap)).map((c) => c.attr),
    ["cn", "uid", "mail", "sn", "givenName", "telephoneNumber", "departmentNumber", "objectClass", "createTimestamp", "modifyTimestamp"]);
  assert.deepEqual(commonChips(has(ad)).map((c) => c.attr),
    ["cn", "sAMAccountName", "mail", "sn", "givenName", "department", "objectClass", "whenCreated", "whenChanged", "memberOf"]);
  assert.deepEqual(commonChips(() => false), []);
  assert.equal(commonChips(has(ad), 3).length, 3);
});

test("a chip carries its friendly label", () => {
  const chips = commonChips(has(openldap));
  assert.deepEqual(chips[2], { attr: "mail", label: "Email" });
  assert.equal(chips.find((c) => c.attr === "uid").label, "Login");
});

test("what a new condition starts as", () => {
  assert.deepEqual(chipDefaults("mail", { kind: "string" }), { op: "contains", value: "" });
  assert.deepEqual(chipDefaults("createTimestamp", { kind: "time" }), { op: "withinDays", value: "7" });
  assert.deepEqual(chipDefaults("objectClass", { kind: "string" }), { op: "equals", value: "" });
  assert.deepEqual(chipDefaults("member", { kind: "dn" }), { op: "equals", value: "" });
  assert.deepEqual(chipDefaults("enabled", { kind: "boolean" }), { op: "equals", value: "true" });
  assert.deepEqual(chipDefaults("uidNumber", { kind: "integer" }), { op: "equals", value: "" });
  assert.deepEqual(chipDefaults("x", null), { op: "contains", value: "" });
});

test("the options summary reads like a sentence", () => {
  assert.equal(optionsSummary({ base: "ou=people,dc=x", scope: "sub", limit: 500, attrs: "" }),
    "Looking in the whole branch below ou=people,dc=x, showing all attributes, at most 500 results");
  assert.equal(optionsSummary({ base: "ou=people,dc=x", scope: "one", limit: 50, attrs: "uid, mail" }),
    "Looking in only the direct children of ou=people,dc=x, showing uid, mail, at most 50 results");
  assert.equal(optionsSummary({ base: "dc=x", scope: "base", limit: 10, attrs: "cn" }),
    "Looking in only dc=x, showing cn, at most 10 results");
  assert.equal(optionsSummary({ base: "", scope: "sub", limit: 0, attrs: " " }),
    "Looking in the whole directory, showing all attributes, at most 500 results");
  assert.equal(optionsSummary({ base: "dc=x", scope: "galaxy", limit: 5, attrs: "a b" }),
    "Looking in the whole branch below dc=x, showing a, b, at most 5 results");
});
