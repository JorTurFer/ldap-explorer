// The look-and-feel setting (light / dark / follow the system), kept on the server so it
// survives restarts (the browser address changes with every start, so localStorage would not).
import { $ } from "./dom.js";
import { api } from "./api.js";
import { nextTheme, themeLabel, themeAttribute } from "./settings-model.js";

let theme = "auto";

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
  try { theme = (await api("/api/settings")).theme || "auto"; } catch { theme = "auto"; }
  apply();
}
