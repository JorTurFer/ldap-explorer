// The Explore tab: shows what a branch of the directory contains (which kinds of
// entries and which attributes are really in use) so that people who do not
// know the schema can discover it, and turn what they see into a search.

import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";

export function initExplore() {
  on("connected", ({ roots }) => {
    $("btnExplore").disabled = false;
    if (!$("xBase").value && roots[0]) $("xBase").value = roots[0];
  });
  on("disconnected", () => { $("btnExplore").disabled = true; $("xOut").replaceChildren(); });
  on("select", ({ dn }) => { $("xBase").value = dn; }); // the selected tree node is the natural thing to explore
  $("btnExplore").onclick = analyze;
  $("xBase").addEventListener("keydown", (ev) => { if (ev.key === "Enter") analyze(); });
}

async function analyze() {
  const out = $("xOut");
  const base = $("xBase").value.trim();
  const scope = $("xScope").value;
  out.replaceChildren(el("div", { class: "muted", textContent: "Analyzing…" }));
  try {
    const limit = parseInt($("xLimit").value, 10) || 200;
    const s = await api(`/api/sample?base=${encodeURIComponent(base)}&scope=${scope}&limit=${limit}`);
    out.replaceChildren(
      el("div", { class: "muted", id: "xSummary", textContent:
        `Looked at ${s.sampled} entr${s.sampled === 1 ? "y" : "ies"}` + (s.truncated ? ` (stopped at the limit: there may be more)` : "") + "." }),
      ...(s.warning ? [el("div", { class: "warn", textContent: "⚠ " + s.warning })] : []),
      el("div", { class: "xcols" },
        el("div", {}, el("h3", { textContent: "Kinds of entries (object classes)" }),
          list(s.objectClasses, s.sampled, (name) => emit("load-query", {
            title: `Entries of class ${name}`, base, scope, attrs: "",
            query: { op: "equals", attr: "objectClass", value: name } }), "class")),
        el("div", {}, el("h3", { textContent: "Attributes in use" }),
          list(s.attributes, s.sampled, (name) => emit("load-query", {
            title: `Entries that have ${name}`, base, scope, attrs: name,
            query: { op: "present", attr: name } }), "attr"))));
  } catch (e) { out.replaceChildren(el("div", { class: "err", textContent: e.message })); }
}

function list(items, total, onClick, kind) {
  return el("ul", { class: "xlist", id: "xList-" + kind }, ...items.map((u) => el("li", {},
    el("a", { textContent: u.name, title: "Search for these", onclick: () => onClick(u.name) }),
    el("span", { class: "n", textContent: `${u.count} (${Math.round((100 * u.count) / total)}%)` }),
    el("progress", { class: "pbar", max: total, value: u.count }))));
}
