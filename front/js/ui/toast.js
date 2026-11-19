// A short confirmation that disappears by itself ("Copied", "Saved").
import { $, el } from "../dom.js";

const SHOW_MS = 2200;

export function toast(message, kind = "") {
  const host = $("toasts");
  if (!host) return;
  const t = el("div", { class: "toast" + (kind ? " " + kind : ""), textContent: message });
  host.append(t);
  setTimeout(() => t.remove(), SHOW_MS);
}
