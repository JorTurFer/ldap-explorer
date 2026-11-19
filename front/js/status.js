import { $ } from "./dom.js";

export function setStatus(msg, isErr = false) {
  const s = $("status");
  s.textContent = msg;
  s.title = msg;
  s.className = "status" + (isErr ? " err" : "");
}
