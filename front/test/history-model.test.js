import test from "node:test";
import assert from "node:assert/strict";
import { historyLabel, historyTitle } from "../js/history-model.js";

const now = new Date("2026-11-19T12:00:00Z");

test("a history item reads as its filter and when it ran", () => {
  assert.equal(historyLabel({ summary: "(uid=bob)", at: "2026-11-19T11:57:00Z" }, now), "(uid=bob) · 3 minutes ago");
  assert.equal(historyLabel({ summary: "(uid=bob)", at: "2026-11-19T11:59:50Z" }, now), "(uid=bob) · just now");
  assert.equal(historyLabel({ summary: "(uid=bob)", at: "2026-11-17T12:00:00Z" }, now), "(uid=bob) · 2 days ago");
});

test("long filters are shortened, whitespace tidied", () => {
  const long = "(&(objectClass=inetOrgPerson)(|(cn=*a*)(mail=*a*)(uid=*a*)(sn=*a*)(givenName=*a*)))";
  const label = historyLabel({ summary: long, at: "2026-11-19T11:00:00Z" }, now);
  assert.ok(label.startsWith(long.slice(0, 59) + "…"), label);
  assert.ok(label.endsWith("· 1 hour ago"));
  assert.equal(historyLabel({ summary: "(a=1)\n  (b=2)", at: "2026-11-19T11:00:00Z" }, now), "(a=1) (b=2) · 1 hour ago");
});

test("missing parts do not break it", () => {
  assert.equal(historyLabel({ filter: "(x=1)" }, now), "(x=1)");
  assert.equal(historyLabel({}, now), "(no filter)");
  assert.equal(historyLabel({ summary: "(x=1)", at: "garbage" }, now), "(x=1)");
});

test("the tooltip says where and how", () => {
  assert.equal(historyTitle({ base: "ou=p,dc=x", scope: "one", mode: "raw" }), "ou=p,dc=x · one level · typed filter");
  assert.equal(historyTitle({ base: "", scope: "base", mode: "builder" }), "(whole directory) · that entry only · builder");
  assert.equal(historyTitle({ base: "dc=x", scope: "weird", mode: "builder" }), "dc=x · whole branch · builder");
});
