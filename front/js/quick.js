// "Connect as…": one click to switch between saved profiles, i.e. between users
// or between LDAP servers, using the password each one keeps.

import { $, el } from "./dom.js";
import { emit, on } from "./events.js";
import { profileLabel } from "./cred-model.js";
import { savedProfiles } from "./profiles.js";
import { connectProfile } from "./connection.js";

export function initQuick() {
  on("profiles-changed", (ps) => render(ps));
  on("connected", ({ profile }) => { $("quickConn").value = profile.name && savedProfiles().some((p) => p.name === profile.name) ? profile.name : ""; });
  on("disconnected", () => { $("quickConn").value = ""; });

  $("quickConn").onchange = async () => {
    const p = savedProfiles().find((x) => x.name === $("quickConn").value);
    if (!p) return;
    // Nothing saved for this user: ask for the password in the usual dialog.
    if (p.bindDN && p.storage === "none") { emit("open-connect", { profile: p, message: "Enter the password for this user." }); return; }
    try {
      await connectProfile(p, "");
    } catch (e) {
      $("quickConn").value = "";
      emit("open-connect", { profile: p, message: e.message });
    }
  };
}

function render(ps) {
  const sel = $("quickConn");
  const cur = sel.value;
  sel.replaceChildren(el("option", { value: "", textContent: "Connect as…" }),
    ...ps.map((p) => el("option", { value: p.name, textContent: profileLabel(p) })));
  sel.value = ps.some((p) => p.name === cur) ? cur : "";
}
