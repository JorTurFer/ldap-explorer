import { $, el } from "./dom.js";
import { api } from "./api.js";
import { on } from "./events.js";
import { showTab } from "./tabs.js";
import { showResults } from "./results.js";
import { resolveFilter, setQuery } from "./builder.js";

let mode = "builder"; // "builder" | "raw"

function setMode(m) {
  mode = m;
  $("modeBuilder").classList.toggle("on", m === "builder");
  $("modeRaw").classList.toggle("on", m === "raw");
  $("builderBox").classList.toggle("hidden", m !== "builder");
  $("rawBox").classList.toggle("hidden", m !== "raw");
}

const hideNote = () => $("sNote").classList.add("hidden");

async function doSearch() {
  if ($("btnSearch").disabled) return;
  const out = $("sOut");
  out.replaceChildren(el("div", { class: "muted", textContent: "Searching…" }));
  try {
    const filter = mode === "builder" ? (await resolveFilter()).filter : $("sFilter").value.trim();
    const t0 = performance.now();
    const res = await api("/api/search", { body: {
      base: $("sBase").value.trim(), filter, scope: $("sScope").value,
      attrs: $("sAttrs").value, limit: parseInt($("sLimit").value, 10) || 500 } });
    const ms = Math.round(performance.now() - t0);
    showResults(out, res, $("sAttrs").value, ms);
  } catch (e) { out.replaceChildren(el("div", { class: "err mt12", textContent: e.message })); }
}

export function initSearch() {
  on("connected", ({ roots }) => {
    $("btnSearch").disabled = false;
    if (!$("sBase").value && roots[0]) $("sBase").value = roots[0];
  });
  on("disconnected", () => { $("btnSearch").disabled = true; });
  on("search-from", ({ dn }) => { $("sBase").value = dn; showTab("search"); (mode === "raw" ? $("sFilter") : $("sBase")).focus(); });
  $("modeBuilder").onclick = () => setMode("builder");
  $("modeRaw").onclick = () => setMode("raw");
  on("builder-to-raw", ({ filter }) => { $("sFilter").value = filter; setMode("raw"); });
  on("run-search", () => { hideNote(); doSearch(); });
  on("load-query", async (plan) => {
    // A recipe or the explorer hands over a query: show it in the builder, run it.
    if (plan.base) $("sBase").value = plan.base;
    $("sScope").value = plan.scope || "sub";
    $("sAttrs").value = plan.attrs || "";
    await setQuery(plan.query);
    setMode("builder");
    const note = $("sNote");
    note.textContent = plan.title + (plan.note ? " — " + plan.note : "");
    note.classList.remove("hidden");
    showTab("search");
    doSearch();
  });
  $("btnSearch").onclick = () => { hideNote(); doSearch(); };
  for (const id of ["sBase", "sFilter", "sAttrs", "sLimit"])
    $(id).addEventListener("keydown", (ev) => { if (ev.key === "Enter") { hideNote(); doSearch(); } });
}
