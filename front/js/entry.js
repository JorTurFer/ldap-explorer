import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { showTab } from "./tabs.js";

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
    for (const x of a.values || []) v.append(el("div", { class: a.binary ? "bin" : "", textContent: x }));
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
    const e = await api("/api/entry?dn=" + encodeURIComponent(dn) + ($("chkOp").checked ? "&operational=1" : ""));
    pane.replaceChildren(
      el("div", { class: "dn" }, el("span", { textContent: e.dn }), copyBtn(e.dn),
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
