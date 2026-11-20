// The searches you ran recently on this connection: pick one to put it back in the form.
import { $, el } from "./dom.js";
import { api } from "./api.js";
import { on } from "./events.js";
import { applyState } from "./search.js";
import { historyLabel, historyTitle } from "./history-model.js";
import { toast } from "./ui/toast.js";

let items = [];

export function initHistory() {
  on("connected", load);
  on("disconnected", () => { items = []; render(); });
  on("searched", async ({ state, summary }) => {
    try { items = await api("/api/history", { body: { ...state, summary } }); render(); } catch { /* history is a convenience */ }
  });
  $("hSel").onchange = async () => {
    const item = items[Number($("hSel").value)];
    $("hSel").value = "";
    if (!item) return;
    await applyState(item);
    const note = $("sNote");
    note.textContent = "A recent search was loaded. Press Search to run it again.";
    note.classList.remove("hidden");
  };
  $("hClear").onclick = async () => {
    try { items = await api("/api/history", { method: "DELETE" }); render(); toast("History cleared"); } catch (e) { toast(e.message, "err"); }
  };
}

async function load() {
  try { items = await api("/api/history"); } catch { items = []; }
  render();
}

function render() {
  const now = new Date();
  $("hSel").replaceChildren(el("option", { value: "", textContent: items.length ? "— recent searches —" : "— no recent searches —" }),
    ...items.map((it, i) => el("option", { value: String(i), textContent: historyLabel(it, now), title: historyTitle(it) })));
  $("hSel").disabled = items.length === 0;
  $("hClear").disabled = items.length === 0;
}
