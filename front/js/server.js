// The Server tab: what this server is, and a browser for its schema (every attribute and
// object class it accepts, how they relate, and a way to search for entries that use them).

import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { getSchema } from "./schema.js";
import { goTo } from "./tree.js";
import { copyBtn } from "./ui/copy.js";
import { KIND_TEXT, CLASS_KIND_TEXT, usageText, flavorLabel, controlInfo, extensionInfo, filterItems, indexByName, classChain, effectiveAttrs, usedBy, formatCount } from "./schema-model.js";

const MAX_LISTED = 300; // the list is capped so a huge schema stays fast; filtering narrows it
let schema = null;       // the index from schema.js
let mode = "attrs";      // "attrs" | "classes"
let attrIdx = null, classIdx = null;
let selected = null;

export function initServer() {
  on("connected", async () => {
    $("scFilter").disabled = false;
    await Promise.all([loadInfo(), loadSchema()]);
  });
  on("disconnected", () => {
    schema = attrIdx = classIdx = selected = null;
    $("scFilter").disabled = true; $("scFilter").value = "";
    $("srvInfo").replaceChildren(el("div", { class: "empty", textContent: "Connect to a server to see its details." }));
    $("scList").replaceChildren(); $("scCount").textContent = "";
    $("scDetail").replaceChildren(el("div", { class: "empty", textContent: "Select an item on the left." }));
  });
  $("scFilter").addEventListener("input", renderList);
  $("scAttrs").onclick = () => setMode("attrs");
  $("scClasses").onclick = () => setMode("classes");
}

function setMode(m) {
  mode = m;
  selected = null;
  $("scAttrs").classList.toggle("on", m === "attrs");
  $("scClasses").classList.toggle("on", m === "classes");
  $("scDetail").replaceChildren(el("div", { class: "empty", textContent: "Select an item on the left." }));
  renderList();
}

// ---------- this server ----------

const row = (label, ...content) => el("div", { class: "kvrow" }, el("div", { class: "kvlabel", textContent: label }), el("div", { class: "kvvalue" }, ...content));

async function loadInfo() {
  const box = $("srvInfo");
  try {
    const s = await api("/api/server");
    const vendor = [s.vendorName, s.vendorVersion].filter(Boolean).join(" ");
    box.replaceChildren(
      el("h3", { class: "section", textContent: "This server" }),
      row("Type", flavorLabel(s.flavor), s.flavor === "unknown" ? el("span", { class: "muted small", textContent: " (it did not say what it is)" }) : null),
      ...(vendor ? [row("Vendor", vendor)] : []),
      row("LDAP version", (s.supportedLDAPVersion || []).join(", ") || "—"),
      row("Naming contexts", ...((s.namingContexts || []).length
        ? s.namingContexts.map((nc) => el("div", { class: "nc" },
          el("a", { class: "dnlink", textContent: nc, title: "Show it in the tree", onclick: () => goTo(nc) }),
          copyBtn(nc, { title: "Copy this DN", what: "Copied the DN" })))
        : ["—"])),
      row("Features", ...listWithOids((s.supportedControls || []).map(controlInfo), "Controls: ways a search can be refined, such as paging")),
      row("Operations", ...listWithOids((s.supportedExtensions || []).map(extensionInfo))),
      ...((s.supportedSASLMechanisms || []).length ? [row("Sign-in methods (SASL)", (s.supportedSASLMechanisms || []).join(", "))] : []));
  } catch (e) { box.replaceChildren(el("div", { class: "err", textContent: e.message })); }
}

function listWithOids(items, hint) {
  if (items.length === 0) return ["—"];
  return [
    ...(hint ? [el("div", { class: "muted small", textContent: hint })] : []),
    ...items.map((i) => el("div", { class: "oidline" },
      el("span", { textContent: i.name || "(unnamed)" }), el("span", { class: "muted mono small", textContent: " " + i.oid }),
      copyBtn(i.oid, { label: "OID", title: "Copy the OID", what: "Copied the OID" }))),
  ];
}

// ---------- schema ----------

async function loadSchema() {
  $("scList").replaceChildren(el("li", { class: "muted", textContent: "Loading the schema…" }));
  schema = await getSchema();
  if (!schema) {
    $("scList").replaceChildren(el("li", { class: "muted", textContent: "This server does not let you read its schema." }));
    $("scFilter").disabled = true;
    return;
  }
  attrIdx = indexByName(schema.rawAttributes);
  classIdx = indexByName(schema.rawClasses);
  renderList();
}

// "123 attributes", or, when the list is capped, "showing 300 of 412 attributes: type to narrow it down".
// Exported through schema-model.js so it can be tested.
const countText = (n, what) => formatCount(n, MAX_LISTED, what);
const items = () => (mode === "attrs" ? schema.rawAttributes.filter((a) => !a.obsolete) : schema.rawClasses.filter((c) => !c.obsolete));

function renderList() {
  if (!schema) return;
  const found = filterItems(items(), $("scFilter").value);
  const shown = found.slice(0, MAX_LISTED);
  $("scCount").textContent = countText(found.length, mode === "attrs" ? "attributes" : "object classes");
  $("scList").replaceChildren(...shown.map((it) => el("li", { class: "scitem" + (selected === it ? " sel" : ""), "data-name": it.name },
    el("a", { textContent: it.name, onclick: () => select(it) }),
    el("span", { class: "muted small", textContent: it.desc ? " " + it.desc.slice(0, 60) : "" }))));
}

function select(it) {
  selected = it;
  for (const li of $("scList").children) li.classList.toggle("sel", li.dataset.name === it.name);
  $("scDetail").replaceChildren(mode === "attrs" ? attrDetail(it) : classDetail(it));
}

// A link to another attribute or class in the browser.
function link(name, kind) {
  return el("a", { class: "scref", textContent: name, onclick: () => {
    if (kind !== mode) setMode(kind);
    const it = (kind === "attrs" ? attrIdx : classIdx).get(name.toLowerCase());
    if (it) { $("scFilter").value = ""; renderList(); select(it); }
  } });
}

const withCopy = (text, what) => el("span", { class: "has-copy" }, text, copyBtn(text, { title: `Copy ${what}`, what: `Copied ${what}` }));
const ruleText = (r, none) => (r ? r : el("span", { class: "muted", textContent: none }));

function attrDetail(a) {
  const used = usedBy(schema.rawClasses, a.name);
  return el("div", { class: "detail" },
    el("h3", {}, withCopy(a.name, "the name")),
    el("p", { class: "muted", textContent: a.desc || "No description." }),
    row("Other names", (a.names || []).filter((n) => n !== a.name).join(", ") || "—"),
    row("OID", withCopy(a.oid, "the OID")),
    row("Holds", KIND_TEXT[a.kind] || a.kind, a.syntaxName ? el("span", { class: "muted small", textContent: ` · ${a.syntaxName}` }) : null),
    row("Values", a.singleValue ? "one value at most" : "any number of values"),
    row("Kind", usageText(a.usage)),
    ...(a.sup ? [row("Derived from", link(a.sup, "attrs"))] : []),
    row("Can be compared for", el("div", {},
      el("div", {}, "equality: ", ruleText(a.equality, "not supported")),
      el("div", {}, "ordering (before/after): ", ruleText(a.ordering, "not supported")),
      el("div", {}, "contains / starts with: ", ruleText(a.substring, "not supported")))),
    row("Used by", ...(used.length
      ? used.slice(0, 40).map((u) => el("span", { class: "usedby" }, link(u.class, "classes"), u.required ? el("span", { class: "badge", textContent: "required" }) : null, " "))
      : ["no object class lists it"]),
      used.length > 40 ? el("span", { class: "muted small", textContent: ` …and ${used.length - 40} more` }) : null),
    el("div", { class: "entry-actions" },
      el("button", { type: "button", class: "small-btn find", textContent: "Find entries that have it", title: "Search the directory for entries with a value in this attribute",
        onclick: () => emit("load-query", { title: `Entries that have ${a.name}`, attrs: a.name, scope: "sub", query: { op: "present", attr: a.name } }) })));
}

function classDetail(c) {
  const chain = classChain(schema.rawClasses, c.name).slice(1);
  const eff = effectiveAttrs(schema.rawClasses, c.name);
  const attrList = (list) => list.length
    ? list.map((x) => el("span", { class: "usedby" }, link(x.name, "attrs"), x.from !== c.name ? el("span", { class: "muted small", textContent: ` (from ${x.from})` }) : null, " "))
    : [el("span", { class: "muted", textContent: "none" })];
  return el("div", { class: "detail" },
    el("h3", {}, withCopy(c.name, "the name"), el("span", { class: "badge", textContent: c.kind.toLowerCase() })),
    el("p", { class: "muted", textContent: c.desc || "No description." }),
    el("p", { class: "muted small", textContent: CLASS_KIND_TEXT[c.kind] || "" }),
    row("Other names", (c.names || []).filter((n) => n !== c.name).join(", ") || "—"),
    row("OID", withCopy(c.oid, "the OID")),
    row("Derives from", ...(chain.length ? chain.map((p) => el("span", { class: "usedby" }, link(p.name, "classes"), " → ")).concat([el("span", { class: "muted", textContent: "(top)" })]) : ["nothing"])),
    row("Required attributes", ...attrList(eff.filter((x) => x.required))),
    row("Optional attributes", ...attrList(eff.filter((x) => !x.required))),
    el("div", { class: "entry-actions" },
      el("button", { type: "button", class: "small-btn find", textContent: "Find entries of this kind", title: "Search the directory for entries with this object class",
        onclick: () => emit("load-query", { title: `Entries of class ${c.name}`, attrs: "", scope: "sub", query: { op: "equals", attr: "objectClass", value: c.name } }) })));
}
