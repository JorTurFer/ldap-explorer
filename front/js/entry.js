import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { showTab } from "./tabs.js";
import { goTo } from "./tree.js";
import { splitDN } from "./nav.js";
import { getSchema, isDNAttr } from "./schema.js";

let currentDN = null;

export function copyBtn(text) {
  return el("button", { class: "copy", textContent: "copy", onclick: (ev) => {
    ev.stopPropagation(); navigator.clipboard.writeText(text);
    ev.target.textContent = "✓"; setTimeout(() => ev.target.textContent = "copy", 900);
  }});
}

export function attrTable(attrs) {
  const t = el("table", { class: "attrs" });
  for (const a of attrs || []) {
    const v = el("td", { class: "v" });
    const dnAttr = isDNAttr(a.name);
    for (const x of a.values || []) {
      v.append(dnAttr
        ? el("div", {}, el("a", { class: "dnlink", textContent: x, title: "Go to this entry", onclick: () => goTo(x) }))
        : el("div", { class: a.binary ? "bin" : "", textContent: x }));
    }
    t.append(el("tr", {}, el("td", { class: "k", textContent: a.name }), v));
  }
  return t;
}

export async function showEntry(dn) {
  showTab("entry");
  currentDN = dn;
  const pane = $("pane-entry");
  pane.replaceChildren(el("div", { class: "muted", textContent: "Loading…" }));
  try {
    await getSchema(); // so DN-valued attributes can be shown as links
    const e = await api("/api/entry?dn=" + encodeURIComponent(dn) + ($("chkOp").checked ? "&operational=1" : ""));
    pane.replaceChildren(
      el("div", { class: "dn" }, breadcrumb(e.dn), copyBtn(e.dn),
        el("a", { class: "jump", textContent: "search from here", onclick: () => emit("search-from", { dn: e.dn }) })),
      attrTable(e.attrs));
  } catch (err) { pane.replaceChildren(el("div", { class: "err", textContent: err.message })); }
}

export function initEntry() {
  on("select", ({ dn }) => showEntry(dn));
  on("disconnected", () => {
    currentDN = null;
    $("pane-entry").replaceChildren(el("div", { class: "empty", textContent: "Select an entry in the tree." }));
  });
  $("chkOp").onchange = () => { if (currentDN) showEntry(currentDN); };
}

// The DN with every ancestor as a link, so one click goes up the tree. The text
// is exactly the DN the server returned.
function breadcrumb(dn) {
  const parts = splitDN(dn);
  const span = el("span", { class: "crumbs" });
  parts.forEach((p, i) => {
    if (i > 0) span.append(dn.slice(parts[i - 1].end, p.start));
    if (i === 0) span.append(el("span", { textContent: p.rdn })); // the entry itself
    else span.append(el("a", { class: "crumb", textContent: p.rdn, title: "Go to this entry", onclick: () => goTo(dn.slice(p.start).trim()) }));
  });
  return span;
}
