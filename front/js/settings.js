// The look-and-feel setting (light / dark / follow the system), kept on the server so it
// survives restarts (the browser address changes with every start, so localStorage would not).
import { $ } from "./dom.js";
import { api } from "./api.js";
import { emit } from "./events.js";
import { nextTheme, themeLabel, themeAttribute } from "./settings-model.js";

let theme = "auto";
let hints = false; // ask the server which entries have children (see settings.go)

// Does the tree ask which entries have children? Off by default: it is what makes a page of a
// huge branch slow, and without it the tree loads the names only.
export const childHints = () => hints;

function apply() {
  const attr = themeAttribute(theme);
  if (attr) document.documentElement.setAttribute("data-theme", attr);
  else document.documentElement.removeAttribute("data-theme");
  $("btnTheme").textContent = themeLabel(theme);
}

export async function initSettings() {
  $("btnTheme").onclick = async () => {
    theme = nextTheme(theme);
    apply();
    try { await api("/api/settings", { body: { theme } }); } catch { /* the choice still applies to this session */ }
  };
  $("chkHints").onchange = async () => {
    hints = $("chkHints").checked;
    try { await api("/api/settings", { body: { childHints: hints } }); } catch { /* it still applies to this session */ }
    emit("child-hints-changed", { on: hints });
  };
  try { const s = await api("/api/settings"); theme = s.theme || "auto"; hints = !!s.childHints; } catch { theme = "auto"; }
  $("chkHints").checked = hints;
  apply();
}
