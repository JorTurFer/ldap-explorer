import { emit } from "./events.js";

// `meta` rides along on the tab-shown event (showEntry says { fromEntry: true }: the entry view
// opening its own tab is not a new place to go back to; the entry itself is).
export function showTab(name, meta = {}) {
  for (const b of document.querySelectorAll(".tabs button")) b.classList.toggle("on", b.dataset.tab === name);
  for (const p of document.querySelectorAll(".pane")) p.classList.toggle("on", p.id === "pane-" + name);
  emit("tab-shown", { name, ...meta });
}

export function initTabs() {
  for (const b of document.querySelectorAll(".tabs button")) b.onclick = () => showTab(b.dataset.tab);
}
