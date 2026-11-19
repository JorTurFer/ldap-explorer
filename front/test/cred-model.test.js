import test from "node:test";
import assert from "node:assert/strict";
import { storeOptions, storeHint, profileLabel, hostOf, shortUser, needsVault, vaultButtonLabel, stateLabel, validatePassphrases } from "../js/cred-model.js";

const ok = { keychain: { available: true }, vault: { exists: true, unlocked: true } };
const noKeychain = { keychain: { available: false, reason: "no secret service" }, vault: { exists: false, unlocked: false } };
const locked = { keychain: { available: true }, vault: { exists: true, unlocked: false } };

test("store options reflect what the machine can do", () => {
  assert.deepEqual(storeOptions(ok).map((o) => [o.value, o.disabled]), [["none", false], ["keychain", false], ["vault", false]]);
  const o = storeOptions(noKeychain);
  assert.equal(o[1].disabled, true);
  assert.match(o[1].label, /not available here/);
  assert.match(o[2].label, /not created yet/);
  assert.match(storeOptions(locked)[2].label, /locked/);
  assert.doesNotMatch(storeOptions(ok)[2].label, /locked|not created/);
  // before the status is known nothing is disabled
  assert.ok(storeOptions(null).every((x) => !x.disabled));
});

test("hints explain what will happen", () => {
  assert.match(storeHint("none", ok), /type the password every time/);
  assert.match(storeHint("keychain", ok), /system's keychain/);
  assert.match(storeHint("keychain", noKeychain), /no secret service/);
  assert.match(storeHint("vault", noKeychain), /does not exist yet/);
  assert.match(storeHint("vault", locked), /locked/);
  assert.match(storeHint("vault", ok), /unlocked now/);
  assert.equal(storeHint("vault", null), "");
});

test("hostOf and shortUser", () => {
  assert.equal(hostOf("ldaps://ldap.example.org:636"), "ldap.example.org:636");
  assert.equal(hostOf("ldap://10.0.0.1"), "10.0.0.1");
  assert.equal(hostOf("nonsense"), "");
  assert.equal(shortUser("uid=alice,ou=people,dc=example,dc=org"), "alice");
  assert.equal(shortUser("cn=Smith\\, John,ou=p"), "Smith\\, John");
  assert.equal(shortUser("alice@corp.example"), "alice@corp.example");
  assert.equal(shortUser("cn=admin"), "admin");
});

test("quick-switch label shows who and where", () => {
  assert.equal(profileLabel({ name: "lab", url: "ldap://h:389", bindDN: "uid=alice,dc=x" }), "lab — alice @ h:389");
  assert.equal(profileLabel({ name: "anon", url: "ldaps://h", bindDN: "" }), "anon — anonymous @ h");
  assert.equal(profileLabel({ name: "x", url: "", bindDN: "cn=a" }), "x — a");
});

test("which errors mean unlock the vault", () => {
  assert.equal(needsVault({ code: "vault_locked" }), true);
  assert.equal(needsVault({ code: "vault_missing" }), true);
  assert.equal(needsVault({ code: "wrong_passphrase" }), false);
  assert.equal(needsVault(new Error("x")), false);
  assert.equal(needsVault(null), false);
});

test("header button label", () => {
  assert.equal(vaultButtonLabel(null), "Passwords");
  assert.equal(vaultButtonLabel({ vault: { exists: false } }), "Passwords");
  assert.equal(vaultButtonLabel(ok), "Passwords · vault unlocked");
  assert.equal(vaultButtonLabel(locked), "Passwords · vault locked");
});

test("state labels", () => {
  assert.equal(stateLabel("saved"), "saved");
  assert.equal(stateLabel("locked"), "vault locked");
  assert.equal(stateLabel("weird"), "weird");
});

test("passphrase validation counts characters and checks the confirmation when creating", () => {
  assert.match(validatePassphrases("short", "short", true), /at least 8/);
  assert.match(validatePassphrases("ñandú€€", "ñandú€€", true), /at least 8/); // 7 characters
  assert.equal(validatePassphrases("12345678", "12345678", true), "");
  assert.match(validatePassphrases("12345678", "12345679", true), /different/);
  assert.equal(validatePassphrases("12345678", "", false), "");
});
