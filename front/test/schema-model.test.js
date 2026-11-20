import test from "node:test";
import assert from "node:assert/strict";
import { filterItems, indexByName, classChain, effectiveAttrs, usedBy, usageText, flavorLabel, controlInfo, extensionInfo } from "../js/schema-model.js";

const attrs = [
  { name: "cn", names: ["cn", "commonName"], oid: "2.5.4.3", desc: "common name(s) for which the entity is known by" },
  { name: "mail", names: ["mail", "rfc822Mailbox"], oid: "0.9.2342.19200300.100.1.3", desc: "RFC822 Mailbox" },
  { name: "mailPreferenceOption", names: ["mailPreferenceOption"], oid: "0.9.2342.19200300.100.1.47", desc: "" },
  { name: "sn", names: ["sn", "surname"], oid: "2.5.4.4", desc: "last (family) name(s) for which the entity is known by" },
];

test("filter: every word must match, best matches first", () => {
  assert.deepEqual(filterItems(attrs, "mail").map((a) => a.name), ["mail", "mailPreferenceOption"]); // exact name before "starts with"
  assert.deepEqual(filterItems(attrs, "commonname").map((a) => a.name), ["cn"]);                    // an alias matches
  assert.deepEqual(filterItems(attrs, "2.5.4.4").map((a) => a.name), ["sn"]);                        // so does the OID
  assert.deepEqual(filterItems(attrs, "family name").map((a) => a.name), ["sn"]);                    // and the description
  assert.deepEqual(filterItems(attrs, "known entity").map((a) => a.name), ["cn", "sn"]);             // all words, any order
  assert.deepEqual(filterItems(attrs, "nothing like this"), []);
  assert.deepEqual(filterItems(attrs, "  ").map((a) => a.name), ["cn", "mail", "mailPreferenceOption", "sn"]); // empty: everything, alphabetical
  assert.deepEqual(filterItems(attrs, "MAIL").map((a) => a.name), ["mail", "mailPreferenceOption"]);
});

test("filter does not modify its input", () => {
  const copy = JSON.stringify(attrs);
  filterItems(attrs, "");
  filterItems(attrs, "mail");
  assert.equal(JSON.stringify(attrs), copy);
});

const classes = [
  { name: "top", names: ["top"], oid: "2.5.6.0", kind: "ABSTRACT", sup: [], must: ["objectClass"], may: [] },
  { name: "person", names: ["person"], oid: "2.5.6.6", kind: "STRUCTURAL", sup: ["top"], must: ["sn", "cn"], may: ["userPassword", "telephoneNumber", "description"] },
  { name: "organizationalPerson", names: ["organizationalPerson", "organizationalPerson"], oid: "2.5.6.7", kind: "STRUCTURAL", sup: ["person"], must: [], may: ["title", "description"] },
  { name: "inetOrgPerson", names: ["inetOrgPerson"], oid: "2.16.840.1.113730.3.2.2", kind: "STRUCTURAL", sup: ["organizationalPerson"], must: [], may: ["mail", "uid"] },
  { name: "posixAccount", names: ["posixAccount"], oid: "1.3.6.1.1.1.2.0", kind: "AUXILIARY", sup: ["top"], must: ["cn", "uid"], may: ["userPassword"] },
];

test("indexByName finds by name, alias and OID, any case", () => {
  const idx = indexByName(classes);
  assert.equal(idx.get("inetorgperson").name, "inetOrgPerson");
  assert.equal(idx.get("2.5.6.6").name, "person");
  assert.equal(idx.get("nothing"), undefined);
});

test("the chain goes from the class up to top, once each", () => {
  assert.deepEqual(classChain(classes, "inetOrgPerson").map((c) => c.name), ["inetOrgPerson", "organizationalPerson", "person", "top"]);
  assert.deepEqual(classChain(classes, "INETORGPERSON").map((c) => c.name), ["inetOrgPerson", "organizationalPerson", "person", "top"]);
  assert.deepEqual(classChain(classes, "top").map((c) => c.name), ["top"]);
  assert.deepEqual(classChain(classes, "unknown"), []);
  // a cycle in a broken schema must not loop
  const cyc = [{ name: "a", names: ["a"], oid: "1", sup: ["b"] }, { name: "b", names: ["b"], oid: "2", sup: ["a"] }];
  assert.deepEqual(classChain(cyc, "a").map((c) => c.name), ["a", "b"]);
});

test("effective attributes: required first, inherited included, each once", () => {
  const eff = effectiveAttrs(classes, "inetOrgPerson");
  assert.deepEqual(eff.filter((a) => a.required).map((a) => a.name), ["cn", "objectClass", "sn"]);
  assert.deepEqual(eff.filter((a) => !a.required).map((a) => a.name), ["description", "mail", "telephoneNumber", "title", "uid", "userPassword"]);
  assert.equal(eff.find((a) => a.name === "sn").from, "person");
  assert.equal(eff.find((a) => a.name === "mail").from, "inetOrgPerson");
  assert.equal(eff.find((a) => a.name === "description").from, "organizationalPerson"); // the nearest class that lists it
  // required wins when a class lists it as optional and a parent as required
  assert.equal(effectiveAttrs(classes, "posixAccount").find((a) => a.name === "cn").required, true);
  assert.deepEqual(effectiveAttrs(classes, "nope"), []);
});

test("which classes use an attribute", () => {
  assert.deepEqual(usedBy(classes, "cn"), [{ class: "person", required: true, optional: false }, { class: "posixAccount", required: true, optional: false }]);
  assert.deepEqual(usedBy(classes, "DESCRIPTION").map((u) => u.class), ["organizationalPerson", "person"]);
  assert.deepEqual(usedBy(classes, "nothing"), []);
});

test("plain-language texts", () => {
  assert.match(usageText(""), /ordinary/);
  assert.match(usageText("userApplications"), /ordinary/);
  assert.match(usageText("directoryOperation"), /operational \(directoryOperation\)/);
  assert.equal(flavorLabel("ad"), "Active Directory");
  assert.equal(flavorLabel("openldap"), "OpenLDAP");
  assert.match(flavorLabel("whatever"), /Unknown/);
  assert.deepEqual(controlInfo("1.2.840.113556.1.4.319"), { name: "Paged results", oid: "1.2.840.113556.1.4.319" });
  assert.deepEqual(controlInfo("9.9.9"), { name: "", oid: "9.9.9" });
  assert.equal(extensionInfo("1.3.6.1.4.1.1466.20037").name, "StartTLS");
});
