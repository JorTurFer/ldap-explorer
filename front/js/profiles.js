import { $, el } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { emit, on } from "./events.js";
import { storeOptions, storeHint } from "./cred-model.js";
import { getStatus, withVault, refreshStatus } from "./vault.js";
import { friendlyError } from "./friendly-errors.js";

let profiles = [];

export const savedProfiles = () => profiles;

export async function loadProfiles() {
  try { profiles = await api("/api/profiles"); } catch (e) { profiles = []; }
  const sel = $("cProfile"); const cur = sel.value;
  sel.replaceChildren(el("option", { value: "", textContent: "— new —" }),
    ...profiles.map(p => el("option", { value: p.name, textContent: p.name })));
  sel.value = profiles.some(p => p.name === cur) ? cur : "";
  emit("profiles-changed", profiles);
}

export function formProfile() {
  return {
    name: $("cName").value.trim(), url: $("cURL").value.trim(), bindDN: $("cBind").value.trim(),
    baseDN: $("cBase").value.trim(), startTLS: $("cStartTLS").checked, skipVerify: $("cSkip").checked,
    storage: $("cStore").value,
  };
}

export function fillForm(p) {
  $("cName").value = p?.name || ""; $("cURL").value = p?.url || ""; $("cBind").value = p?.bindDN || "";
  $("cBase").value = p?.baseDN || ""; $("cStartTLS").checked = !!p?.startTLS; $("cSkip").checked = !!p?.skipVerify;
  $("cStore").value = p?.storage || "none"; $("cPass").value = "";
  updateStoreUI();
}

// The password box says what happens when it is left empty.
function updateStoreUI() {
  const storage = $("cStore").value;
  $("cPass").placeholder = storage !== "none" ? "empty = use the saved one" : "";
  $("cStoreHint").textContent = storeHint(storage, getStatus());
}

function renderStoreOptions(status) {
  const cur = $("cStore").value || "none";
  $("cStore").replaceChildren(...storeOptions(status).map((o) =>
    el("option", { value: o.value, textContent: o.label, disabled: o.disabled })));
  $("cStore").value = cur;
  updateStoreUI();
}

export function initProfiles() {
  renderStoreOptions(null);
  on("vault-status", renderStoreOptions);
  on("profiles-stale", loadProfiles);
  $("cStore").onchange = async () => { await refreshStatus(); updateStoreUI(); };
  $("cProfile").onchange = () => fillForm(profiles.find(p => p.name === $("cProfile").value));

  $("btnSaveProfile").onclick = async () => {
    $("cErr").textContent = "";
    try {
      const p = formProfile(); if (!p.name) throw new Error("Give the profile a name");
      const password = $("cPass").value;
      await withVault(() => api("/api/profiles", { body: { profile: p, password } }));
      await loadProfiles(); $("cProfile").value = p.name; setStatus("Profile saved");
      $("cPass").value = "";
      await refreshStatus();
    } catch (e) { $("cErr").textContent = friendlyError(e.message); }
  };

  // Keep the server's settings and add another user: for several accounts on one LDAP.
  $("btnDupProfile").onclick = () => {
    const p = formProfile();
    fillForm({ url: p.url, startTLS: p.startTLS, skipVerify: p.skipVerify, baseDN: p.baseDN, storage: p.storage });
    $("cProfile").value = "";
    $("cErr").textContent = "Same server: enter the other user's name, DN and password, then Save profile.";
    $("cName").focus();
  };

  $("btnDelProfile").onclick = async () => {
    const n = $("cProfile").value; if (!n) return;
    await api("/api/profiles?name=" + encodeURIComponent(n), { method: "DELETE" });
    await loadProfiles(); fillForm(null);
    await refreshStatus();
  };
}
