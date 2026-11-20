// The status bar at the bottom: what the program is doing right now (a search, loading a
// branch...), for how long, how many entries have arrived, how long it usually takes, and
// whether it looks stuck. The decisions are in activity-model.js; this draws them.
import { $, el } from "./dom.js";
import { api } from "./api.js";
import { tracker, cancelAll, cancelOne } from "./activity-core.js";
import { clockTime } from "./activity-model.js";
import { on } from "./events.js";

let lost = false;        // the program itself stopped answering
let drawn = "";          // what was drawn last, to touch the page only when something changes
let announced = "";      // what a screen reader was last told

let open = false;        // the list of requests is expanded
const liveRows = new Map(); // id -> { li, text, detail, bar, note } of the running rows, updated in place
let historySig = null;

export function initActivity() {
  $("actCancel").onclick = (ev) => { ev.stopPropagation(); cancelAll(); };
  // The whole bar opens and closes the list (its buttons do their own thing).
  $("activity").addEventListener("click", (ev) => { if (!ev.target.closest("button")) toggle(); });
  $("actToggle").onclick = toggle;
  $("actClear").onclick = () => { tracker.clearHistory(); historySig = null; render(); };
  on("activity-changed", render);
  setInterval(render, 250);
  setInterval(poll, 1000);
  render();
}

function toggle() {
  open = !open;
  $("activityPanel").hidden = !open;
  $("actToggle").setAttribute("aria-expanded", String(open));
  $("actToggle").textContent = open ? "▼ Requests" : "▲ Requests";
  historySig = null; drawn = "";
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

// The expanded list: running requests (rows updated in place, so a Cancel button never moves
// under the mouse) and the finished ones.
function renderPanel() {
  const l = tracker.list(performance.now());
  const ul = $("actRunning");
  const seen = new Set();
  for (const r of l.running) {
    seen.add(r.id);
    let row = liveRows.get(r.id);
    if (!row) {
      row = {
        spinner: el("span", { class: "spinner", "aria-hidden": "true" }),
        text: el("span", { class: "act-row-text" }),
        detail: el("span", { class: "act-row-detail muted" }),
        bar: el("progress", { max: 1, "aria-label": "Progress of this request" }),
        note: el("span", { class: "act-row-note" }),
        cancel: el("button", { type: "button", class: "small-btn", textContent: "Cancel", title: "Stop waiting for this request", onclick: () => cancelOne(r.id) }),
      };
      row.li = el("li", { class: "act-row running" }, row.spinner, row.text, row.bar, row.detail, row.note, row.cancel);
      liveRows.set(r.id, row);
      ul.append(row.li);
    }
    row.li.className = `act-row running ${r.level}`;
    row.text.textContent = r.label; row.text.title = r.label;
    row.detail.textContent = r.parts.join(" · ");
    row.note.textContent = r.note; row.note.title = r.note;
    if (r.fraction === null) row.bar.removeAttribute("value"); else row.bar.value = r.fraction;
  }
  for (const [id, row] of liveRows) if (!seen.has(id)) { row.li.remove(); liveRows.delete(id); }

  const sig = l.history.map((h) => `${h.startedWall}|${h.outcome}|${h.ms}`).join(";");
  if (sig !== historySig) {
    historySig = sig;
    $("actHistory").replaceChildren(...l.history.map((h) => el("li", { class: `act-row done ${h.outcome}` },
      el("span", { class: "spinner", "aria-hidden": "true" }),
      el("span", { class: "act-row-text", textContent: h.label, title: h.label }),
      el("span", { class: "act-row-detail muted", textContent: h.text }),
      el("span", { class: "act-row-when muted", textContent: clockTime(h.startedWall), title: "When it started" }))));
  }
  $("actEmpty").hidden = l.running.length + l.history.length > 0;
  $("actClear").disabled = l.history.length === 0;
}

function render() {
  if (open) renderPanel();
  const v = tracker.view(performance.now());
  const level = lost ? "stuck" : v.level;
  const text = lost ? "Lost contact with ldap-explorer: it is not answering. Close this tab and start it again if it does not come back."
    : v.busy ? v.label : v.done ? `${v.done.text} — ${v.done.label}` : "Ready";
  const detail = v.busy ? v.parts.join(" · ") + (v.others ? ` · +${v.others} more` : "") : "";
  const count = tracker.list(performance.now()).running.length;
  $("actToggle").textContent = `${open ? "▼" : "▲"} Requests${count > 1 ? ` (${count} running)` : ""}`;
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
