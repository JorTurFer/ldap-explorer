import { emit } from "./events.js";

export function showTab(name) {
  for (const b of document.querySelectorAll(".tabs button")) b.classList.toggle("on", b.dataset.tab === name);
  for (const p of document.querySelectorAll(".pane")) p.classList.toggle("on", p.id === "pane-" + name);
  emit("tab-shown", { name });
}

export function initTabs() {
  for (const b of document.querySelectorAll(".tabs button")) b.onclick = () => showTab(b.dataset.tab);
}
