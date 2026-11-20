import { $ } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { emit, on } from "./events.js";
import { loadProfiles, formProfile, fillForm } from "./profiles.js";
import { withVault, refreshStatus } from "./vault.js";
import { friendlyError } from "./friendly-errors.js";

// Connects with a profile. When the profile keeps its password somewhere, an empty
// password means "use the saved one" (the server reads it; it never travels back).
export async function connectProfile(p, password) {
  const res = await withVault(() => api("/api/connect", { body: { profile: p, password } }));
  connected(p, res.roots || []);
}

export function initConnection() {
  $("btnConn").onclick = () => openDialog();
  $("btnCancel").onclick = () => $("dlg").close();
  on("open-connect", ({ profile, message }) => openDialog(profile, message));

  $("cform").onsubmit = async (ev) => {
    ev.preventDefault();
    $("cErr").textContent = ""; $("btnDoConn").disabled = true;
    try {
      await connectProfile(formProfile(), $("cPass").value);
      $("cPass").value = "";
      $("dlg").close();
    } catch (e) { $("cErr").textContent = friendlyError(e.message); }
    finally { $("btnDoConn").disabled = false; }
  };
  $("btnDisc").onclick = async () => {
    await api("/api/disconnect", { method: "POST" }).catch(() => {});
    $("btnDisc").disabled = true;
    setStatus("Not connected");
    emit("disconnected");
  };
}

async function openDialog(profile, message) {
  $("cErr").textContent = message || "";
  await loadProfiles();
  await refreshStatus();
  if (profile) { fillForm(profile); $("cProfile").value = profile.name || ""; }
  $("dlg").showModal();
  if (profile) $("cPass").focus();
}

function connected(p, roots) {
  $("btnDisc").disabled = false;
  setStatus(`Connected to ${p.url}` + (p.bindDN ? ` as ${p.bindDN}` : " (anonymous)"));
  emit("connected", { profile: p, roots });
}
