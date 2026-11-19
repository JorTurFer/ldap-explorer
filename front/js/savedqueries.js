// Saved queries of the current connection (stored server side, next to the
// profiles, never with credentials).

import { $, el } from "./dom.js";
import { api } from "./api.js";
import { on } from "./events.js";
import { currentState, applyState } from "./search.js";

let queries = [];

export function initSavedQueries() {
  on("connected", async () => {
    $("qSave").disabled = false;
    await refresh();
  });
  on("disconnected", () => {
    queries = [];
    $("qSave").disabled = $("qDelete").disabled = true;
    $("qName").value = ""; $("qMsg").textContent = "";
    renderSelect("");
  });

  $("qSaved").onchange = async () => {
    const q = queries.find((x) => x.name === $("qSaved").value);
    $("qDelete").disabled = !q;
    $("qName").value = q ? q.name : "";
    if (!q) return;
    await applyState(q);
    const note = $("sNote");
    note.textContent = `Saved query "${q.name}" loaded. Press Search to run it.`;
    note.classList.remove("hidden");
  };

  $("qSave").onclick = async () => {
    const name = $("qName").value.trim();
    const msg = $("qMsg");
    msg.textContent = "";
    const state = currentState();
    if (state.mode === "builder" && !state.query) { msg.textContent = "Add at least one condition first."; return; }
    try {
      queries = await api("/api/queries", { body: { ...state, name } });
      renderSelect(name);
      msg.textContent = "Saved.";
    } catch (e) { msg.textContent = e.message; }
  };

  $("qDelete").onclick = async () => {
    const name = $("qSaved").value;
    if (!name) return;
    try {
      queries = await api("/api/queries?name=" + encodeURIComponent(name), { method: "DELETE" });
      $("qName").value = "";
      renderSelect("");
      $("qMsg").textContent = "Deleted.";
    } catch (e) { $("qMsg").textContent = e.message; }
  };
}

async function refresh() {
  try { queries = await api("/api/queries"); } catch (e) { queries = []; $("qMsg").textContent = e.message; }
  renderSelect("");
}

function renderSelect(selected) {
  const sel = $("qSaved");
  sel.replaceChildren(el("option", { value: "", textContent: "— saved queries —" }),
    ...queries.map((q) => el("option", { value: q.name, textContent: q.name })));
  sel.value = queries.some((q) => q.name.toLowerCase() === selected.toLowerCase())
    ? queries.find((q) => q.name.toLowerCase() === selected.toLowerCase()).name : "";
  $("qDelete").disabled = !sel.value;
}
