import { $ } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { emit } from "./events.js";
import { loadProfiles, formProfile } from "./profiles.js";

export function initConnection() {
  $("btnConn").onclick = async () => { $("cErr").textContent = ""; await loadProfiles(); $("dlg").showModal(); };
  $("btnCancel").onclick = () => $("dlg").close();
  $("cform").onsubmit = async (ev) => {
    ev.preventDefault();
    $("cErr").textContent = ""; $("btnDoConn").disabled = true;
    try {
      const p = formProfile();
      const res = await api("/api/connect", { body: { profile: p, password: $("cPass").value } });
      $("cPass").value = "";
      $("dlg").close();
      connected(p, res.roots || []);
    } catch (e) { $("cErr").textContent = e.message; }
    finally { $("btnDoConn").disabled = false; }
  };
  $("btnDisc").onclick = async () => {
    await api("/api/disconnect", { method: "POST" }).catch(() => {});
    $("btnDisc").disabled = true;
    setStatus("Not connected");
    emit("disconnected");
  };
}

function connected(p, roots) {
  $("btnDisc").disabled = false;
  setStatus(`Connected to ${p.url}` + (p.bindDN ? ` as ${p.bindDN}` : " (anonymous)"));
  emit("connected", { profile: p, roots });
}
