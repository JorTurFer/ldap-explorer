// Pure model of the query builder: no DOM, unit-tested with `node --test`.
//
// A group is { type: "group", mode: "all" | "any" | "none", items: [...] }.
// A condition is { type: "condition", attr, op, value, valueType? } where valueType
// (set by the UI from the schema) is "datetime" for time attributes, whose typed
// local value is converted to UTC.
// toQuery() turns that into the tree /api/filter understands.

export const OPS = {
  equals: "is",
  notEquals: "is not",
  contains: "contains",
  startsWith: "starts with",
  endsWith: "ends with",
  wildcard: "matches pattern (use *)",
  present: "exists",
  notPresent: "does not exist",
  gte: "is at least",
  lte: "is at most",
  after: "is after",
  before: "is before",
  withinDays: "is in the last (days)",
};

// UI operator -> API operator.
const API_OP = { after: "gte", before: "lte" };

// Which operators make sense for an attribute. `info` comes from the schema
// ({ kind, canEqual, canOrder, canSubstring }); null means "not known".
export function opsFor(info) {
  if (!info) return ["equals", "notEquals", "contains", "startsWith", "endsWith", "wildcard", "present", "notPresent"];
  const ops = [];
  const kind = info.kind;
  if (kind === "binary") return ["present", "notPresent"];
  if (info.canEqual) ops.push("equals", "notEquals");
  if (info.canSubstring && kind !== "time" && kind !== "integer" && kind !== "boolean" && kind !== "dn") {
    ops.push("contains", "startsWith", "endsWith", "wildcard");
  }
  if (info.canOrder) {
    if (kind === "time") ops.push("after", "before");
    else if (kind === "integer") ops.push("gte", "lte");
  }
  if (kind === "time") ops.push("withinDays");
  ops.push("present", "notPresent");
  return ops;
}

// Shown when the server's schema declares no matching rules for an attribute (Active Directory
// never does): the options offered are a guess from the type of the field.
export const RULES_UNKNOWN_NOTE = "The server does not say how this field can be compared (Active Directory never does), so the usual options for this kind of field are offered. If a search finds nothing, the server may not support that option for it.";

export const defaultOp = (info) => (opsFor(info).includes("contains") ? "contains" : opsFor(info)[0]);

// What kind of input the value needs: none | text | boolean | datetime | integer | days.
export function valueKind(info, op) {
  if (op === "present" || op === "notPresent") return "none";
  if (op === "withinDays") return "days";
  if (!info) return "text";
  if (info.kind === "boolean") return "boolean";
  if (info.kind === "time") return "datetime";
  if (info.kind === "integer") return "integer";
  return "text";
}

export const newCondition = () => ({ type: "condition", attr: "", op: "contains", value: "" });
export const newGroup = (mode = "all") => ({ type: "group", mode, items: [newCondition()] });

// Is there anything to run? A condition counts when it names an attribute.
export const isEmptyQuery = (g) => g.items.every((it) => (it.type === "group" ? isEmptyQuery(it) : !it.attr.trim()));

// "2026-11-19T10:30" typed in the browser's local time -> "2026-11-19T09:30:00Z".
// offsetMinutes is Date.getTimezoneOffset() (positive when behind UTC); passing
// it in keeps this function pure.
export function localToUTC(local, offsetMinutes) {
  const m = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2}))?)?$/.exec((local || "").trim());
  if (!m) return local; // let the server explain what is wrong
  const [, y, mo, d, h = "0", mi = "0", s = "0"] = m;
  const t = Date.UTC(+y, +mo - 1, +d, +h, +mi, +s) + offsetMinutes * 60000;
  return new Date(t).toISOString().replace(/\.\d{3}Z$/, "Z");
}

// Builds the API query. Conditions without an attribute are skipped; a group
// with nothing left is skipped too.
export function toQuery(g, offsetMinutes = 0) {
  const items = [];
  for (const it of g.items) {
    if (it.type === "group") {
      const q = toQuery(it, offsetMinutes);
      if (q) items.push(q);
      continue;
    }
    if (!it.attr.trim()) continue;
    let value = it.value;
    if (it.op === "after" || it.op === "before" || it.valueType === "datetime") {
      value = localToUTC(value, offsetMinutes);
    }
    items.push({ op: API_OP[it.op] || it.op, attr: it.attr.trim(), value });
  }
  if (items.length === 0) return null;
  if (g.mode === "none") return { op: "not", items: [{ op: "or", items }] };
  return { op: g.mode === "any" ? "or" : "and", items };
}

// Inverse of toQuery for a tree coming from a recipe or a saved query.
export function fromQuery(q) {
  const OP_UI = { gte: "gte", lte: "lte" };
  const cond = (n) => ({ type: "condition", attr: n.attr || "", op: OP_UI[n.op] || n.op, value: n.value || "" });
  const conv = (n) => {
    if (n.op === "and" || n.op === "or") return { type: "group", mode: n.op === "or" ? "any" : "all", items: (n.items || []).map(conv) };
    if (n.op === "not") {
      // NOT(OR(...)) is how "none of" is stored
      const inner = (n.items || [])[0];
      if (inner && inner.op === "or") return { type: "group", mode: "none", items: (inner.items || []).map(conv) };
      return { type: "group", mode: "none", items: (n.items || []).map(conv) };
    }
    return cond(n);
  };
  const root = conv(q);
  return root.type === "group" ? root : { type: "group", mode: "all", items: [root] };
}

// When a recipe or a saved query is loaded, time comparisons arrive as gte/lte on a
// time attribute: show them with the friendlier "is after / is before".
export function friendlyOps(g, infoFor) {
  for (const it of g.items) {
    if (it.type === "group") { friendlyOps(it, infoFor); continue; }
    const info = infoFor(it.attr);
    if (info && info.kind === "time") {
      if (it.op === "gte") it.op = "after";
      else if (it.op === "lte") it.op = "before";
    }
  }
  return g;
}
