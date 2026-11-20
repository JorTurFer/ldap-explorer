// The status bar at the bottom: what the program is doing right now (a search, loading a
// branch...), for how long, how many entries have arrived, how long it usually takes, and
// whether it looks stuck. The decisions are in activity-model.js; this draws them.
import { $, el } from "./dom.js";
import { api } from "./api.js";
import { tracker, cancelAll } from "./activity-core.js";
import { on } from "./events.js";

let lost = false;        // the program itself stopped answering
let drawn = "";          // what was drawn last, to touch the page only when something changes
let announced = "";      // what a screen reader was last told

export function initActivity() {
  $("actCancel").onclick = cancelAll;
  on("activity-changed", render);
  setInterval(render, 250);
  setInterval(poll, 1000);
  render();
}

// Ask the server how far the running requests are (pages and entries of a paged search).
async function poll() {
  if (!tracker.ids().length) { lost = false; return; }
  try {
    const { ops } = await api("/api/activity");
    lost = false;
    const now = performance.now();
    for (const o of ops) tracker.progress(o.id, { entries: o.entries, pages: o.pages, quietMs: o.quietMs }, now);
  } catch { lost = true; }
  render();
}

function render() {
  const v = tracker.view(performance.now());
  const level = lost ? "stuck" : v.level;
  const text = lost ? "Lost contact with ldap-explorer: it is not answering. Close this tab and start it again if it does not come back."
    : v.busy ? v.label : v.done ? `${v.done.text} — ${v.done.label}` : "Ready";
  const detail = v.busy ? v.parts.join(" · ") + (v.others ? ` · +${v.others} more` : "") : "";
  const state = [v.busy || lost ? "busy" : v.done ? "done" : "idle", level, text, detail, v.note, v.fraction === null ? "-" : Math.round(v.fraction * 100)].join("|");
  if (state === drawn) return;
  drawn = state;

  const bar = $("activity");
  bar.className = `activity ${v.busy || lost ? "busy" : v.done ? "done" : "idle"} ${level}`;
  $("actText").textContent = text;
  $("actText").title = text;
  $("actDetail").textContent = detail;
  $("actNote").textContent = lost ? "" : v.note;
  $("actNote").title = lost ? "" : v.note;
  const p = $("actProgress");
  p.hidden = !v.busy || lost;
  if (v.fraction === null) p.removeAttribute("value"); else p.value = v.fraction; // no value = the bar just moves
  $("actCancel").hidden = !v.busy;

  // Tell a screen reader only when the state changes (start, slow, stuck, done), not every tick.
  const say = lost ? text : v.busy ? `${v.label}. ${v.note}` : v.done ? `${v.done.text}. ${v.done.label}` : "";
  if (say !== announced) { announced = say; $("actLive").textContent = say; }
}
