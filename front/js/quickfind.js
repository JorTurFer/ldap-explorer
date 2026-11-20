// The "Find anyone or anything" box in the header: type a name, login or email, press
// Enter, and the "Find any entry" recipe runs. The simplest way to search.
import { $ } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { toast } from "./ui/toast.js";

export function initQuickFind() {
  const box = $("quickFind");
  on("connected", () => { box.disabled = false; });
  on("disconnected", () => { box.disabled = true; box.value = ""; });
  box.addEventListener("keydown", async (ev) => {
    if (ev.key === "Escape") { box.value = ""; box.blur(); return; }
    if (ev.key !== "Enter") return;
    const text = box.value.trim();
    if (!text) return;
    try {
      emit("load-query", await api("/api/recipes/find-entry", { body: { params: { text } } }));
    } catch (e) { toast(e.message, "err"); }
  });
}
