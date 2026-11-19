import { $, el } from "./dom.js";
import { api } from "./api.js";
import { on } from "./events.js";
import { showTab } from "./tabs.js";
import { attrTable, showEntry } from "./entry.js";

async function doSearch() {
  if ($("btnSearch").disabled) return;
  const out = $("sOut");
  out.replaceChildren(el("div", { class: "muted", textContent: "Searching…" }));
  try {
    const t0 = performance.now();
    const res = await api("/api/search", { body: {
      base: $("sBase").value.trim(), filter: $("sFilter").value.trim(), scope: $("sScope").value,
      attrs: $("sAttrs").value, limit: parseInt($("sLimit").value, 10) || 500 } });
    const ms = Math.round(performance.now() - t0);
    const items = res.entries.map(e => el("details", { class: "result" },
      el("summary", {}, e.dn, el("a", { class: "jump", textContent: "view", onclick: (ev) => { ev.preventDefault(); showEntry(e.dn); } })),
      el("div", { class: "body" }, attrTable(e.attrs))));
    out.replaceChildren(
      el("div", { class: "muted mt12", textContent: `${res.entries.length} result(s) in ${ms} ms` }),
      ...(res.warning ? [el("div", { class: "warn", textContent: "⚠ " + res.warning })] : []),
      ...items);
    if (items.length === 1) items[0].open = true;
  } catch (e) { out.replaceChildren(el("div", { class: "err mt12", textContent: e.message })); }
}

export function initSearch() {
  on("connected", ({ roots }) => {
    $("btnSearch").disabled = false;
    if (!$("sBase").value && roots[0]) $("sBase").value = roots[0];
  });
  on("disconnected", () => { $("btnSearch").disabled = true; });
  on("search-from", ({ dn }) => { $("sBase").value = dn; showTab("search"); $("sFilter").focus(); });
  $("btnSearch").onclick = doSearch;
  for (const id of ["sBase", "sFilter", "sAttrs", "sLimit"])
    $(id).addEventListener("keydown", (ev) => { if (ev.key === "Enter") doSearch(); });
}
