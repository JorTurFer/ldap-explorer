// Keyboard shortcuts. The key logic is in shortcuts-model.js; this wires it to the page.
import { $, el } from "./dom.js";
import { showTab } from "./tabs.js";
import { SHORTCUTS, step, isTypingTarget, describeKeys } from "./shortcuts-model.js";

let pending = null;
let swallow = null; // the character of a shortcut that moved the focus into a box: it must not be typed there

const actions = {
  quickfind: () => { if (!$("quickFind").disabled) $("quickFind").focus(); },
  treefilter: () => { if (!$("treeFilter").disabled) $("treeFilter").focus(); },
  bookmarks: () => { $("bookmarks").open = true; $("bookmarks").scrollIntoView({ block: "nearest" }); },
  passwords: () => $("btnVault").click(),
  back: () => { const b = $("navBack"); if (b && !b.disabled) b.click(); },
  forward: () => { const b = $("navForward"); if (b && !b.disabled) b.click(); },
  help: () => openHelp(),
};

function run(action) {
  if (action.startsWith("tab:")) showTab(action.slice(4));
  else actions[action]?.();
}

function openHelp() {
  $("helpTable").replaceChildren(el("tbody", {}, ...SHORTCUTS.map((s) => el("tr", {},
    el("td", {}, ...s.keys.flatMap((k, i) => [i ? " then " : "", el("span", { class: "kbd", textContent: k })])),
    el("td", { textContent: s.help })))));
  $("helpDlg").showModal();
}

export function initShortcuts() {
  $("btnHelp").onclick = openHelp;
  $("helpClose").onclick = () => $("helpDlg").close();
  document.addEventListener("keydown", (ev) => {
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return;                       // browser and OS shortcuts are not ours
    if (isTypingTarget(ev.target.tagName, ev.target.isContentEditable)) return;
    if (document.querySelector("dialog[open]")) return;                       // a dialog has the focus of attention
    const r = step(pending, ev.key, performance.now());
    pending = r.pending;
    if (r.action) {
      ev.preventDefault();
      swallow = ev.key.length === 1 ? ev.key : null;
      run(r.action);
    }
  });
  // Some browsers insert the character even though keydown was cancelled, and into the box that
  // just received the focus ("/" would end up typed in the search box): cancel that insertion.
  document.addEventListener("beforeinput", (ev) => {
    if (swallow !== null && ev.data === swallow) ev.preventDefault();
    swallow = null;
  });
  document.addEventListener("keyup", () => { swallow = null; });
}

export { describeKeys };
