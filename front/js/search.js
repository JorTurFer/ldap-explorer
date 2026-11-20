import { $, el } from "./dom.js";
import { api } from "./api.js";
import { on } from "./events.js";
import { showTab } from "./tabs.js";
import { showResults } from "./results.js";
import { resolveFilter, setQuery, getQuery } from "./builder.js";
import { optionsSummary } from "./labels.js";

let mode = "builder"; // "builder" | "raw"

function setMode(m) {
  mode = m;
  $("modeBuilder").classList.toggle("on", m === "builder");
  $("modeRaw").classList.toggle("on", m === "raw");
  $("builderBox").classList.toggle("hidden", m !== "builder");
  $("rawBox").classList.toggle("hidden", m !== "raw");
}

// The state of the search form, as saved queries store it.
export function currentState() {
  const state = { mode, base: $("sBase").value.trim(), scope: $("sScope").value, attrs: $("sAttrs").value.trim(),
    limit: parseInt($("sLimit").value, 10) || 500 };
  if (mode === "builder") state.query = getQuery(); else state.filter = $("sFilter").value.trim();
  return state;
}

// Puts a saved query back into the form (it does not run it).
export async function applyState(q) {
  $("sBase").value = q.base || "";
  $("sScope").value = q.scope || "sub";
  $("sAttrs").value = q.attrs || "";
  $("sLimit").value = q.limit || 500;
  refreshOptionsSummary();
  if (q.mode === "raw") { $("sFilter").value = q.filter || ""; setMode("raw"); }
  else { await setQuery(q.query); setMode("builder"); }
}

// A sentence under "Where to look and what to show" so the options can stay folded.
export function refreshOptionsSummary() {
  $("sOptSummary").textContent = "· " + optionsSummary({ base: $("sBase").value, scope: $("sScope").value, limit: parseInt($("sLimit").value, 10), attrs: $("sAttrs").value });
}

const hideNote = () => $("sNote").classList.add("hidden");

async function doSearch() {
  if ($("btnSearch").disabled) return;
  const out = $("sOut");
  out.replaceChildren(el("div", { class: "muted", textContent: "Searching…" }));
  try {
    const filter = mode === "builder" ? (await resolveFilter()).filter : $("sFilter").value.trim();
    const t0 = performance.now();
    const body = { base: $("sBase").value.trim(), filter, scope: $("sScope").value,
      attrs: $("sAttrs").value, limit: parseInt($("sLimit").value, 10) || 500 };
    const res = await api("/api/search", { body });
    const ms = Math.round(performance.now() - t0);
    showResults(out, res, $("sAttrs").value, ms, body);
  } catch (e) { out.replaceChildren(el("div", { class: "err mt12", textContent: e.message })); }
}

export function initSearch() {
  on("connected", ({ roots }) => {
    $("btnSearch").disabled = false;
    if (!$("sBase").value && roots[0]) $("sBase").value = roots[0];
    refreshOptionsSummary();
  });
  on("disconnected", () => { $("btnSearch").disabled = true; });
  on("search-from", ({ dn }) => { $("sBase").value = dn; refreshOptionsSummary(); showTab("search"); (mode === "raw" ? $("sFilter") : $("sBase")).focus(); });
  for (const id of ["sBase", "sAttrs", "sScope", "sLimit"]) $(id).addEventListener("input", refreshOptionsSummary);
  $("sScope").addEventListener("change", refreshOptionsSummary);
  refreshOptionsSummary();
  $("modeBuilder").onclick = () => setMode("builder");
  $("modeRaw").onclick = () => setMode("raw");
  on("builder-to-raw", ({ filter }) => { $("sFilter").value = filter; setMode("raw"); });
  on("run-search", () => { hideNote(); doSearch(); });
  on("load-query", async (plan) => {
    // A recipe or the explorer hands over a query: show it in the builder, run it.
    if (plan.base) $("sBase").value = plan.base;
    $("sScope").value = plan.scope || "sub";
    $("sAttrs").value = plan.attrs || "";
    refreshOptionsSummary();
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
