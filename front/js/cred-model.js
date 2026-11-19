// Pure helpers for the password-storage UI. No DOM access: unit-tested with `node --test`.

export const STORES = {
  none: "Do not save (type it each time)",
  keychain: "System keychain",
  vault: "Encrypted vault (master passphrase)",
};

// Text and state of the three options of the "Password kept" selector, from
// GET /api/credentials/status.
export function storeOptions(status) {
  const kc = status?.keychain, v = status?.vault;
  return [
    { value: "none", label: STORES.none, disabled: false },
    { value: "keychain", label: STORES.keychain + (kc && !kc.available ? " (not available here)" : ""), disabled: !!kc && !kc.available },
    { value: "vault", label: STORES.vault + (v && !v.exists ? " (not created yet)" : v && !v.unlocked ? " (locked)" : ""), disabled: false },
  ];
}

// A one-line explanation under the selector.
export function storeHint(storage, status) {
  const kc = status?.keychain, v = status?.vault;
  switch (storage) {
    case "keychain":
      return kc && !kc.available ? `The system keychain cannot be used here: ${kc.reason || "unknown reason"}.` : "The password goes to your system's keychain.";
    case "vault":
      if (!v) return "";
      if (!v.exists) return "The vault does not exist yet: you will be asked to create it with a master passphrase.";
      if (!v.unlocked) return "The vault is locked: you will be asked for the master passphrase.";
      return "The password goes to the encrypted vault (unlocked now).";
    default:
      return "Nothing is saved: you type the password every time.";
  }
}

// "alice — uid=alice,… @ ldap.example.org" for the quick switch.
export function profileLabel(p) {
  const host = hostOf(p.url);
  const user = p.bindDN ? shortUser(p.bindDN) : "anonymous";
  return `${p.name} — ${user}${host ? " @ " + host : ""}`;
}

export function hostOf(url) {
  const m = /^ldaps?:\/\/([^/]+)/i.exec((url || "").trim());
  return m ? m[1] : "";
}

// The first RDN value of a DN ("uid=alice,ou=people,..." -> "alice"); AD-style
// "user@domain" is kept as it is.
export function shortUser(dn) {
  if (!dn.includes("=")) return dn;
  const first = dn.split(/(?<!\\),/)[0];
  const eq = first.indexOf("=");
  return eq < 0 ? dn : first.slice(eq + 1).trim();
}

// Which errors mean "unlock (or create) the vault, then try again"?
export const needsVault = (err) => err?.code === "vault_locked" || err?.code === "vault_missing";

// Header button text.
export function vaultButtonLabel(status) {
  const v = status?.vault;
  if (!v || !v.exists) return "Passwords";
  return v.unlocked ? "Passwords · vault unlocked" : "Passwords · vault locked";
}

// A human description of one saved-password state.
export function stateLabel(state) {
  return ({
    saved: "saved",
    missing: "nothing stored yet",
    locked: "vault locked",
    unavailable: "keychain not available",
    anonymous: "anonymous: no password",
    none: "not saved",
  })[state] || state;
}

// What the passphrase dialog should show.
export function validatePassphrases(pass, confirm, creating) {
  if ([...pass].length < 8) return "Use at least 8 characters.";
  if (creating && pass !== confirm) return "The two passphrases are different.";
  return "";
}
