// Back and Forward for the whole program: the buttons in the header, the keys [ and ], Alt+←
// and Alt+→, and the back and forward buttons of a mouse. It remembers the entries you opened and
// the tabs you went to (the logic is in trail.js) and takes you to the same place again.
import { $ } from "./dom.js";
import { on } from "./events.js";
import { showTab } from "./tabs.js";
import { goTo } from "./tree.js";
import { shownDN } from "./entry.js";
import { createTrail, visit, back, forward, canBack, canForward, current, peekBack, peekForward, placeLabel } from "./trail.js";

let trail = createTrail();

const record = (place) => { trail = visit(trail, place); paint(); };

function paint() {
  const prev = peekBack(trail), next = peekForward(trail);
  $("navBack").disabled = !canBack(trail);
  $("navForward").disabled = !canForward(trail);
  $("navBack").title = prev ? `Back to ${placeLabel(prev)}  ( [ )` : "Nothing to go back to";
  $("navForward").title = next ? `Forward to ${placeLabel(next)}  ( ] )` : "Nothing to go forward to";
}

// Show a place. The tab or the entry then announces itself, and since the trail is already
// there, that announcement is not recorded again as something new.
async function show(place) {
  if (place.tab === "entry" && place.dn) await goTo(place.dn);
  else showTab(place.tab);
}

function go(step) {
  const next = step(trail);
  if (next === trail) return;
  trail = next;
  paint();
  show(current(trail));
}

export function initNavigation() {
  $("navBack").onclick = () => go(back);
  $("navForward").onclick = () => go(forward);
  on("select", ({ dn }) => record({ tab: "entry", dn }));
  on("tab-shown", ({ name, fromEntry }) => {
    if (name !== "entry") { record({ tab: name }); return; }
    // The Entry tab was opened by the user: it shows the entry that is selected. (When the entry
    // view opens its own tab, the entry is recorded by the select event instead.)
    const dn = shownDN();
    if (!fromEntry && dn) record({ tab: "entry", dn });
  });
  on("disconnected", () => { trail = createTrail(); paint(); });

  // The mouse's back / forward buttons, and Alt+← / Alt+→ (what a browser does with them).
  const intercept = (ev) => {
    if (ev.button === 3) { ev.preventDefault(); go(back); }
    else if (ev.button === 4) { ev.preventDefault(); go(forward); }
  };
  document.addEventListener("mouseup", intercept);
  document.addEventListener("auxclick", (ev) => { if (ev.button === 3 || ev.button === 4) ev.preventDefault(); });
  document.addEventListener("keydown", (ev) => {
    if (!ev.altKey || ev.ctrlKey || ev.metaKey || ev.shiftKey) return;
    if (ev.key === "ArrowLeft") { ev.preventDefault(); go(back); }
    else if (ev.key === "ArrowRight") { ev.preventDefault(); go(forward); }
  });
  paint();
}
