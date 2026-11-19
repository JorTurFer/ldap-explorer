import { $, el } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";

let profiles = [];

export async function loadProfiles() {
  try { profiles = await api("/api/profiles"); } catch (e) { profiles = []; }
  const sel = $("cProfile"); const cur = sel.value;
  sel.replaceChildren(el("option", { value: "", textContent: "— new —" }),
    ...profiles.map(p => el("option", { value: p.name, textContent: p.name })));
  sel.value = profiles.some(p => p.name === cur) ? cur : "";
}

export function formProfile() {
  return {
    name: $("cName").value.trim(), url: $("cURL").value.trim(), bindDN: $("cBind").value.trim(),
    baseDN: $("cBase").value.trim(), startTLS: $("cStartTLS").checked, skipVerify: $("cSkip").checked,
    useKeychain: $("cKeychain").checked,
  };
}

function fillForm(p) {
  $("cName").value = p?.name || ""; $("cURL").value = p?.url || ""; $("cBind").value = p?.bindDN || "";
  $("cBase").value = p?.baseDN || ""; $("cStartTLS").checked = !!p?.startTLS; $("cSkip").checked = !!p?.skipVerify;
  $("cKeychain").checked = !!p?.useKeychain; $("cPass").value = "";
  $("cPass").placeholder = p?.useKeychain ? "empty = use the one in the keychain" : "";
}

export function initProfiles() {
  $("cProfile").onchange = () => fillForm(profiles.find(p => p.name === $("cProfile").value));
  $("btnSaveProfile").onclick = async () => {
    $("cErr").textContent = "";
    try {
      const p = formProfile(); if (!p.name) throw new Error("Give the profile a name");
      await api("/api/profiles", { body: { profile: p, password: $("cPass").value } });
      await loadProfiles(); $("cProfile").value = p.name; setStatus("Profile saved");
    } catch (e) { $("cErr").textContent = e.message; }
  };
  $("btnDelProfile").onclick = async () => {
    const n = $("cProfile").value; if (!n) return;
    await api("/api/profiles?name=" + encodeURIComponent(n), { method: "DELETE" });
    await loadProfiles(); fillForm(null);
  };
}
