// What the entry view needs to decide, with no DOM: which attributes are operational (the
// ones the server keeps for itself), what they mean in plain words, and which values are
// links to other entries. Pure: unit-tested with `node --test`.

import { looksLikeDN } from "./nav.js";

// Plain-language meaning of the operational attributes people meet most. Keys are lower case.
export const OPERATIONAL_HINTS = {
  entryuuid: "A unique id the server gave this entry. It never changes, even if the entry is renamed.",
  entrydn: "The full DN of this entry, as the server computes it.",
  createtimestamp: "When the entry was created.",
  modifytimestamp: "When the entry was last changed.",
  creatorsname: "Who created the entry.",
  modifiersname: "Who changed the entry last.",
  entrycsn: "A change sequence number the servers use to replicate this entry.",
  structuralobjectclass: "The main type of the entry (the one that decides what it fundamentally is).",
  subschemasubentry: "The entry that holds the schema (the rules for attributes and types) that applies here.",
  hassubordinates: "TRUE when the entry has children below it.",
  numsubordinates: "How many children the entry has.",
  pwdchangedtime: "When the password was last changed.",
  pwdaccountlockedtime: "Since when the account is locked by the password policy.",
  pwdfailuretime: "When the recent failed logins happened.",
  pwdreset: "TRUE when the password must be changed at the next login.",
  pwdpolicysubentry: "The password policy that applies to this entry.",
  memberof: "The groups this entry belongs to, kept up to date by the server.",
  nsaccountlock: "TRUE when the account is disabled (389 Directory Server).",
  nsuniqueid: "A unique id the server gave this entry (389 Directory Server).",
  whencreated: "When the entry was created (Active Directory).",
  whenchanged: "When the entry was last changed (Active Directory).",
  objectguid: "A unique id the server gave this entry (Active Directory).",
  useraccountcontrol: "Flags of the account, such as disabled or password never expires (Active Directory).",
  lastlogontimestamp: "Roughly when the account last logged in (Active Directory; updated only every few days).",
};

export const operationalHint = (name) => OPERATIONAL_HINTS[(name || "").trim().toLowerCase()] || "";

// Splits the attributes of an entry into the ordinary ones and the operational ones, keeping
// the order of each. `isOperational(name)` answers from the server's schema (it may return
// undefined when the schema does not know the attribute): then the names we know decide.
export function splitAttrs(attrs, isOperational = () => undefined) {
  const user = [];
  const operational = [];
  for (const a of attrs || []) {
    const bySchema = isOperational(a.name);
    const op = bySchema === undefined || bySchema === null ? Object.hasOwn(OPERATIONAL_HINTS, a.name.toLowerCase()) && !USER_WHEN_PRESENT.has(a.name.toLowerCase()) : bySchema;
    (op ? operational : user).push(a);
  }
  return { user, operational };
}

// Some attributes are in OPERATIONAL_HINTS because they are worth explaining, but they are
// ordinary (user) attributes when the server stores them in the entry (AD's whenCreated...).
// Without a schema we show those with the ordinary ones.
const USER_WHEN_PRESENT = new Set(["whencreated", "whenchanged", "objectguid", "useraccountcontrol", "lastlogontimestamp", "nsaccountlock", "memberof"]);

// Attributes whose values are DNs even when the schema is not available (or does not say).
export const KNOWN_DN_ATTRS = new Set([
  "member", "uniquemember", "owner", "manager", "secretary", "seealso", "memberof", "roleoccupant",
  "distinguishedname", "managedby", "directreports", "creatorsname", "modifiersname", "entrydn",
  "pwdpolicysubentry", "subschemasubentry", "aliasedobjectname", "dseeassociation",
]);

// Is this value a link to another entry? The schema decides when it knows the attribute;
// otherwise the name has to be one of the well-known DN attributes and the value must look
// like a DN (so a description that happens to contain "a=b" is never turned into a link).
export function isDNValue(name, value, schemaKind) {
  if (schemaKind === "dn") return true;
  if (schemaKind) return false; // the schema knows it, and it is not a DN
  return KNOWN_DN_ATTRS.has((name || "").trim().toLowerCase()) && looksLikeDN(value);
}
