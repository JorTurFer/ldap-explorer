// The password vault: state, the create / unlock dialog, and the "Passwords"
// panel with what is saved for each profile. The server does the cryptography;
// the browser only ever sends a passphrase once, to unlock, and never receives
// a password back.

import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { needsVault, vaultButtonLabel, stateLabel, validatePassphrases, STORES } from "./cred-model.js";

let status = null;

export const getStatus = () => status;

export async function refreshStatus() {
  try { status = await api("/api/credentials/status"); } catch { return status; }
  $("btnVault").textContent = vaultButtonLabel(status);
  emit("vault-status", status);
  return status;
}

// Runs fn; if it needs the vault, asks the user to unlock (or create) it and runs fn again once.
export async function withVault(fn) {
  try {
    return await fn();
  } catch (e) {
    if (!needsVault(e)) throw e;
    if (!(await promptVault(e.code === "vault_missing" ? "create" : "unlock"))) throw new Error("The vault is needed for this and was not unlocked.");
    return await fn();
  }
}

export function initVault() {
  $("btnVault").onclick = openManage;
  $("uCancel").onclick = () => $("unlockDlg").close();
  refreshStatus();
  setInterval(refreshStatus, 20000); // the vault locks itself when idle: keep the button honest
  on("disconnected", refreshStatus);

  $("mClose").onclick = () => $("manageDlg").close();
  $("mLock").onclick = async () => { await api("/api/vault/lock", { method: "POST" }); await renderManage(); };
  $("mUnlock").onclick = async () => { if (await promptVault("unlock")) await renderManage(); };
  $("mCreate").onclick = async () => { if (await promptVault("create")) await renderManage(); };
  $("mChange").onclick = () => { $("changeForm").classList.remove("hidden"); $("mOld").focus(); };
  $("mChangeCancel").onclick = () => { $("changeForm").classList.add("hidden"); $("mErr").textContent = ""; };
  $("changeForm").onsubmit = async (ev) => {
    ev.preventDefault();
    $("mErr").textContent = "";
    const problem = validatePassphrases($("mNew").value, $("mNew2").value, true);
    if (problem) { $("mErr").textContent = problem; return; }
    try {
      await api("/api/vault/passphrase", { body: { oldPassphrase: $("mOld").value, newPassphrase: $("mNew").value } });
      for (const id of ["mOld", "mNew", "mNew2"]) $(id).value = "";
      $("changeForm").classList.add("hidden");
      $("mErr").textContent = "";
      await renderManage();
    } catch (e) { $("mErr").textContent = e.message; }
  };
}

// ---------- create / unlock ----------

// Shows the passphrase dialog. Resolves true once the vault is created/unlocked, false if cancelled.
export function promptVault(mode) {
  const creating = mode === "create";
  const dlg = $("unlockDlg");
  $("unlockTitle").textContent = creating ? "Create the vault" : "Unlock the vault";
  $("unlockText").textContent = creating
    ? "The vault keeps your LDAP passwords in an encrypted file protected by a master passphrase. There is no way to recover them if you forget it."
    : "Enter the master passphrase to use the saved passwords. It locks again by itself after a while without use.";
  $("uOk").textContent = creating ? "Create" : "Unlock";
  $("uConfirm").classList.toggle("hidden", !creating);
  $("uConfirmLabel").classList.toggle("hidden", !creating);
  $("uPass").value = ""; $("uConfirm").value = ""; $("uErr").textContent = "";

  return new Promise((resolve) => {
    let done = false;
    const finish = (ok) => { if (!done) { done = true; resolve(ok); } };
    dlg.addEventListener("close", () => finish(false), { once: true });
    $("unlockForm").onsubmit = async (ev) => {
      ev.preventDefault();
      $("uErr").textContent = "";
      const problem = validatePassphrases($("uPass").value, $("uConfirm").value, creating);
      if (problem) { $("uErr").textContent = problem; return; }
      try {
        await api(creating ? "/api/vault/create" : "/api/vault/unlock", { body: { passphrase: $("uPass").value } });
        $("uPass").value = ""; $("uConfirm").value = "";
        finish(true);
        dlg.close();
        await refreshStatus();
      } catch (e) { $("uErr").textContent = e.message; }
    };
    dlg.showModal();
    $("uPass").focus();
  });
}

// ---------- the Passwords panel ----------

async function openManage() {
  $("mErr").textContent = "";
  $("changeForm").classList.add("hidden");
  await renderManage();
  $("manageDlg").showModal();
}

async function renderManage() {
  const st = await refreshStatus();
  const v = st?.vault || {};
  $("mStatus").textContent = !v.exists
    ? "There is no vault yet. Create one to keep passwords in an encrypted file (useful where there is no system keychain)."
    : v.unlocked
      ? `The vault is unlocked. It locks itself after ${Math.round(v.idleTimeoutSeconds / 60)} minutes without use.`
      : "The vault is locked.";
  $("mCreate").classList.toggle("hidden", !!v.exists);
  $("mUnlock").classList.toggle("hidden", !v.exists || v.unlocked);
  $("mLock").classList.toggle("hidden", !v.unlocked);
  $("mChange").classList.toggle("hidden", !v.unlocked);

  let list;
  try { list = await api("/api/credentials"); } catch (e) { $("mList").replaceChildren(el("div", { class: "err", textContent: e.message })); return; }
  if (list.entries.length === 0) {
    $("mList").replaceChildren(el("div", { class: "muted", textContent: "No saved profiles yet." }));
    return;
  }
  const forget = (entry) => async () => {
    $("mErr").textContent = "";
    try {
      await withVault(() => api("/api/credentials?profile=" + encodeURIComponent(entry.profile), { method: "DELETE" }));
      emit("profiles-stale");
      await renderManage();
    } catch (e) { $("mErr").textContent = e.message; }
  };
  const rows = list.entries.map((e) => el("tr", { "data-profile": e.profile },
    el("td", { textContent: e.profile }),
    el("td", { textContent: e.bindDN || "(anonymous)" }),
    el("td", { textContent: e.url }),
    el("td", { textContent: e.storage === "none" ? "—" : STORES[e.storage] ? (e.storage === "keychain" ? "Keychain" : "Vault") : e.storage }),
    el("td", { class: "state", textContent: stateLabel(e.state) }),
    el("td", {}, e.storage !== "none" && e.bindDN
      ? el("button", { type: "button", class: "forget small-btn", textContent: "Forget password", onclick: forget(e) }) : null)));
  const table = el("table", { class: "creds" },
    el("thead", {}, el("tr", {}, ...["Profile", "User", "Server", "Kept in", "State", ""].map((t) => el("th", { textContent: t })))),
    el("tbody", {}, ...rows));
  const parts = [table];
  if (list.orphans.length) {
    parts.push(el("div", { class: "orphans" },
      el("div", { textContent: "Entries in the vault that no profile uses any more:" }),
      ...list.orphans.map((a) => el("div", { class: "orphan" }, el("span", { textContent: a }),
        el("button", { type: "button", class: "small-btn", textContent: "Remove", onclick: async () => {
          try { await api("/api/credentials?account=" + encodeURIComponent(a), { method: "DELETE" }); await renderManage(); }
          catch (e) { $("mErr").textContent = e.message; }
        } })))));
  }
  $("mList").replaceChildren(...parts);
}
