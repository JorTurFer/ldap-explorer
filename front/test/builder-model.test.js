import test from "node:test";
import assert from "node:assert/strict";
import { opsFor, defaultOp, valueKind, newCondition, newGroup, isEmptyQuery, localToUTC, toQuery, fromQuery, friendlyOps } from "../js/builder-model.js";

const str = { kind: "string", canEqual: true, canOrder: false, canSubstring: true };
const int = { kind: "integer", canEqual: true, canOrder: true, canSubstring: false };
const intNoOrder = { kind: "integer", canEqual: true, canOrder: false, canSubstring: false };
const time = { kind: "time", canEqual: true, canOrder: true, canSubstring: false };
const bool = { kind: "boolean", canEqual: true, canOrder: false, canSubstring: false };
const dn = { kind: "dn", canEqual: true, canOrder: false, canSubstring: false };
const bin = { kind: "binary", canEqual: false, canOrder: false, canSubstring: false };
const oc = { kind: "string", canEqual: true, canOrder: false, canSubstring: false }; // objectClass

test("operators follow what the attribute and the server allow", () => {
  assert.deepEqual(opsFor(str), ["equals", "notEquals", "contains", "startsWith", "endsWith", "wildcard", "present", "notPresent"]);
  assert.deepEqual(opsFor(int), ["equals", "notEquals", "gte", "lte", "present", "notPresent"]);
  assert.deepEqual(opsFor(intNoOrder), ["equals", "notEquals", "present", "notPresent"]);
  assert.deepEqual(opsFor(time), ["equals", "notEquals", "after", "before", "withinDays", "present", "notPresent"]);
  assert.deepEqual(opsFor(bool), ["equals", "notEquals", "present", "notPresent"]);
  assert.deepEqual(opsFor(dn), ["equals", "notEquals", "present", "notPresent"]);
  assert.deepEqual(opsFor(oc), ["equals", "notEquals", "present", "notPresent"]);
  assert.deepEqual(opsFor(bin), ["present", "notPresent"]);
});

test("an unknown attribute gets the generic text operators", () => {
  assert.ok(opsFor(null).includes("contains"));
  assert.ok(!opsFor(null).includes("gte"));
});

test("default operator: contains when possible, otherwise the first one", () => {
  assert.equal(defaultOp(str), "contains");
  assert.equal(defaultOp(oc), "equals");
  assert.equal(defaultOp(bin), "present");
  assert.equal(defaultOp(null), "contains");
});

test("value input kind", () => {
  assert.equal(valueKind(str, "contains"), "text");
  assert.equal(valueKind(str, "present"), "none");
  assert.equal(valueKind(str, "notPresent"), "none");
  assert.equal(valueKind(bool, "equals"), "boolean");
  assert.equal(valueKind(time, "after"), "datetime");
  assert.equal(valueKind(time, "withinDays"), "days");
  assert.equal(valueKind(int, "gte"), "integer");
  assert.equal(valueKind(null, "equals"), "text");
});

test("localToUTC converts using the given offset", () => {
  // Madrid in winter is UTC+1: getTimezoneOffset() = -60
  assert.equal(localToUTC("2026-11-19T10:30", -60), "2026-11-19T09:30:00Z");
  // New York in winter is UTC-5: +300
  assert.equal(localToUTC("2026-11-19T10:30", 300), "2026-11-19T15:30:00Z");
  assert.equal(localToUTC("2026-11-19", 0), "2026-11-19T00:00:00Z");
  assert.equal(localToUTC("2026-11-19T00:30", -60), "2026-11-18T23:30:00Z"); // crosses midnight
  assert.equal(localToUTC("2026-11-19 10:30:15", 0), "2026-11-19T10:30:15Z");
  assert.equal(localToUTC("not a date", 0), "not a date"); // the server reports it
});

test("toQuery: all / any / none, nesting, and skipping empty conditions", () => {
  const g = newGroup("all");
  g.items = [
    { type: "condition", attr: "mail", op: "contains", value: "example" },
    { type: "condition", attr: "  ", op: "equals", value: "ignored" },
    { type: "group", mode: "any", items: [
      { type: "condition", attr: "uid", op: "equals", value: "alice" },
      { type: "condition", attr: "uid", op: "equals", value: "bob" },
    ] },
    { type: "group", mode: "none", items: [{ type: "condition", attr: "mail", op: "present", value: "" }] },
    { type: "group", mode: "all", items: [{ type: "condition", attr: "", op: "equals", value: "" }] }, // empty: dropped
  ];
  assert.deepEqual(toQuery(g), {
    op: "and",
    items: [
      { op: "contains", attr: "mail", value: "example" },
      { op: "or", items: [{ op: "equals", attr: "uid", value: "alice" }, { op: "equals", attr: "uid", value: "bob" }] },
      { op: "not", items: [{ op: "or", items: [{ op: "present", attr: "mail", value: "" }] }] },
    ],
  });
});

test("toQuery: friendly time operators map to gte/lte and dates become UTC", () => {
  const g = newGroup("all");
  g.items = [
    { type: "condition", attr: "createTimestamp", op: "after", value: "2026-11-19T10:30" },
    { type: "condition", attr: "createTimestamp", op: "before", value: "2026-11-20" },
    { type: "condition", attr: "createTimestamp", op: "withinDays", value: "7" },
    { type: "condition", attr: "uidNumber", op: "gte", value: "1000" },
  ];
  assert.deepEqual(toQuery(g, -60).items, [
    { op: "gte", attr: "createTimestamp", value: "2026-11-19T09:30:00Z" },
    { op: "lte", attr: "createTimestamp", value: "2026-11-19T23:00:00Z" },
    { op: "withinDays", attr: "createTimestamp", value: "7" },
    { op: "gte", attr: "uidNumber", value: "1000" }, // numbers are not shifted
  ]);
});

test("toQuery: equals on a time attribute also converts to UTC (valueType from the schema)", () => {
  const g = newGroup("all");
  g.items = [
    { type: "condition", attr: "createTimestamp", op: "equals", value: "2026-11-19T10:30", valueType: "datetime" },
    { type: "condition", attr: "cn", op: "equals", value: "2026-11-19T10:30" }, // a text attribute is never converted
  ];
  assert.deepEqual(toQuery(g, -60).items, [
    { op: "equals", attr: "createTimestamp", value: "2026-11-19T09:30:00Z" },
    { op: "equals", attr: "cn", value: "2026-11-19T10:30" },
  ]);
});

test("toQuery returns null when there is nothing to run", () => {
  assert.equal(toQuery(newGroup()), null);
  assert.equal(isEmptyQuery(newGroup()), true);
  const g = newGroup();
  g.items[0].attr = "cn";
  assert.equal(isEmptyQuery(g), false);
});

test("fromQuery is the inverse of toQuery (recipes and saved queries)", () => {
  const q = {
    op: "and",
    items: [
      { op: "equals", attr: "objectClass", value: "person" },
      { op: "or", items: [{ op: "contains", attr: "cn", value: "a" }, { op: "contains", attr: "mail", value: "a" }] },
      { op: "not", items: [{ op: "or", items: [{ op: "present", attr: "mail" }] }] },
    ],
  };
  const model = fromQuery(q);
  assert.equal(model.mode, "all");
  assert.equal(model.items[1].mode, "any");
  assert.equal(model.items[2].mode, "none");
  assert.deepEqual(toQuery(model), { op: "and", items: [
    { op: "equals", attr: "objectClass", value: "person" },
    { op: "or", items: [{ op: "contains", attr: "cn", value: "a" }, { op: "contains", attr: "mail", value: "a" }] },
    { op: "not", items: [{ op: "or", items: [{ op: "present", attr: "mail", value: "" }] }] },
  ] });
});

test("fromQuery wraps a single condition in a group", () => {
  const model = fromQuery({ op: "equals", attr: "uid", value: "alice" });
  assert.equal(model.type, "group");
  assert.equal(model.items.length, 1);
  assert.equal(model.items[0].attr, "uid");
});

test("friendlyOps shows time comparisons as after/before", () => {
  const model = fromQuery({ op: "and", items: [
    { op: "gte", attr: "createTimestamp", value: "2026-01-01" },
    { op: "lte", attr: "createTimestamp", value: "2026-02-01" },
    { op: "gte", attr: "gidNumber", value: "10" },
  ] });
  friendlyOps(model, (a) => (a === "createTimestamp" ? time : int));
  assert.deepEqual(model.items.map((i) => i.op), ["after", "before", "gte"]);
});

test("newCondition starts empty", () => {
  assert.deepEqual(newCondition(), { type: "condition", attr: "", op: "contains", value: "" });
});
