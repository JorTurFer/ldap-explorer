// The visual query builder: rows of "attribute · operator · value", combined
// with all / any / none, with a live preview of the LDAP filter it produces.
// The filter itself is built by the server (/api/filter), which escapes values
// and checks them against the schema; this module only edits the model.

import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { getSchema, attrInfo } from "./schema.js";
import { OPS, opsFor, defaultOp, valueKind, newCondition, newGroup, toQuery, fromQuery, friendlyOps, isEmptyQuery } from "./builder-model.js";

let model = newGroup("all");
let schemaIdx = null;
let previewTimer = null;
let previewSeq = 0;

const tzOffset = () => new Date().getTimezoneOffset();

export function initBuilder() {
  on("connected", async () => {
    schemaIdx = await getSchema();
    fillDatalists();
    render(); // operators may change now that the schema is known
  });
  on("disconnected", () => { schemaIdx = null; fillDatalists(); });
  $("bToRaw").onclick = async () => {
    try { emit("builder-to-raw", { filter: (await resolveFilter()).filter }); } catch (e) { showErrors([e.message]); }
  };
  render();
}

// Replaces the builder content, e.g. when a recipe or a saved query is loaded.
export async function setQuery(query) {
  schemaIdx = schemaIdx || await getSchema();
  model = friendlyOps(fromQuery(query), (a) => attrInfo(schemaIdx, a));
  render();
}

export const getQuery = () => toQuery(model, tzOffset());

// Asks the server for the filter of the current model. Rejects with a readable
// message when the query is empty or invalid.
export async function resolveFilter() {
  const query = toQuery(model, tzOffset());
  if (!query) throw new Error("Add at least one condition (choose an attribute).");
  return api("/api/filter", { body: { query } });
}

function fillDatalists() {
  $("attrList").replaceChildren(...(schemaIdx ? schemaIdx.names.map((n) => el("option", { value: n })) : []));
  $("classList").replaceChildren(...(schemaIdx ? schemaIdx.classes.map((n) => el("option", { value: n })) : []));
}

// ---------- rendering ----------

function render() {
  $("builder").replaceChildren(renderGroup(model, null, true));
  schedulePreview(0);
}

function renderGroup(g, parent, isRoot) {
  const mode = el("select", { class: "bmode", title: "How the conditions are combined", onchange: (ev) => { g.mode = ev.target.value; render(); } },
    el("option", { value: "all", textContent: "all of" }),
    el("option", { value: "any", textContent: "any of" }),
    el("option", { value: "none", textContent: "none of" }));
  mode.value = g.mode;

  const header = el("div", { class: "bhead" }, el("span", { textContent: "Match " }), mode, el("span", { textContent: " these conditions:" }));
  if (!isRoot) {
    header.append(el("button", { class: "bremove", type: "button", title: "Remove this group", textContent: "×",
      onclick: () => { parent.items.splice(parent.items.indexOf(g), 1); if (parent.items.length === 0) parent.items.push(newCondition()); render(); } }));
  }

  const body = el("div", { class: "bbody" }, ...g.items.map((it) => (it.type === "group" ? renderGroup(it, g, false) : renderCondition(it, g))));
  const footer = el("div", { class: "bfoot" },
    el("button", { type: "button", class: "badd", textContent: "+ Condition", onclick: () => { g.items.push(newCondition()); render(); } }),
    el("button", { type: "button", class: "badd", textContent: "+ Group", onclick: () => { g.items.push(newGroup("any")); render(); } }));
  return el("div", { class: "bgroup" }, header, body, footer);
}

function renderCondition(it, group) {
  const info = attrInfo(schemaIdx, it.attr);
  const ops = opsFor(info);
  if (!ops.includes(it.op)) it.op = defaultOp(info);
  const vk = valueKind(info, it.op);
  it.valueType = vk === "datetime" ? "datetime" : undefined;

  const attr = el("input", { type: "text", class: "battr", placeholder: "attribute, e.g. mail", value: it.attr, autocomplete: "off", spellcheck: false });
  attr.setAttribute("list", "attrList");
  attr.addEventListener("input", () => { it.attr = attr.value; schedulePreview(); });
  // "change" fires when the field loses focus or a suggestion is picked: the type may have changed
  attr.addEventListener("change", () => { it.attr = attr.value; it.value = ""; render(); });

  const op = el("select", { class: "bop", title: "Operator", onchange: (ev) => { it.op = ev.target.value; it.value = ""; render(); } },
    ...ops.map((o) => el("option", { value: o, textContent: OPS[o] })));
  op.value = it.op;

  const remove = el("button", { class: "bremove", type: "button", title: "Remove this condition", textContent: "×",
    onclick: () => { group.items.splice(group.items.indexOf(it), 1); if (group.items.length === 0) group.items.push(newCondition()); render(); } });

  return el("div", { class: "bcond" }, attr, op, valueControl(it, vk), remove);
}

function valueControl(it, vk) {
  if (vk === "none") return el("span", { class: "bval" });
  if (vk === "boolean") {
    if (it.value !== "true" && it.value !== "false") it.value = "true";
    const s = el("select", { class: "bval", onchange: (ev) => { it.value = ev.target.value; schedulePreview(); } },
      el("option", { value: "true", textContent: "true" }), el("option", { value: "false", textContent: "false" }));
    s.value = it.value;
    return s;
  }
  const props = { class: "bval", value: it.value, autocomplete: "off" };
  if (vk === "datetime") props.type = "datetime-local";
  else if (vk === "integer" || vk === "days") { props.type = "number"; props.step = "1"; if (vk === "days") { props.min = "1"; props.placeholder = "number of days"; } }
  else { props.type = "text"; props.placeholder = "value"; props.spellcheck = false; }
  const input = el("input", props);
  if (vk === "text" && it.attr.trim().toLowerCase() === "objectclass") input.setAttribute("list", "classList");
  input.addEventListener("input", () => { it.value = input.value; schedulePreview(); });
  input.addEventListener("keydown", (ev) => { if (ev.key === "Enter") emit("run-search"); });
  return input;
}

// ---------- live preview ----------

function schedulePreview(delay = 200) {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(updatePreview, delay);
}

async function updatePreview() {
  const seq = ++previewSeq;
  if (isEmptyQuery(model) || !toQuery(model, tzOffset())) {
    $("bFilter").textContent = "";
    $("bExplain").textContent = "Choose an attribute to start.";
    showErrors([]);
    return;
  }
  try {
    const res = await api("/api/filter", { body: { query: toQuery(model, tzOffset()) } });
    if (seq !== previewSeq) return; // a newer edit superseded this one
    $("bFilter").textContent = res.filter;
    $("bExplain").textContent = res.explain + (res.schemaAvailable ? "" : "\n(The server hides its schema, so attributes and operators were not checked.)");
    showErrors([]);
  } catch (e) {
    if (seq !== previewSeq) return;
    $("bFilter").textContent = "";
    $("bExplain").textContent = "";
    showErrors([e.message]);
  }
}

function showErrors(messages) {
  $("bErr").replaceChildren(...messages.map((m) => el("div", { textContent: m })));
}
