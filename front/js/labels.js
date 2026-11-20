// Friendly names for common LDAP attributes, the "quick add" chips of the query builder
// and the plain-language summary of the search options. Pure: unit-tested with `node --test`.

const LABELS = {
  cn: "Full name", sn: "Surname", givenname: "First name", displayname: "Display name", name: "Name",
  uid: "Login", samaccountname: "Windows login", userprincipalname: "User principal name",
  mail: "Email", telephonenumber: "Phone", mobile: "Mobile phone", title: "Job title",
  department: "Department", departmentnumber: "Department", employeenumber: "Employee number", employeetype: "Employee type",
  manager: "Manager", description: "Description",
  l: "City", st: "State", c: "Country", street: "Street", postalcode: "Postal code",
  o: "Organization", ou: "Unit",
  objectclass: "Type of entry", memberof: "Member of", member: "Members", uniquemember: "Members", memberuid: "Member logins",
  createtimestamp: "Created", modifytimestamp: "Last changed", whencreated: "Created", whenchanged: "Last changed",
  pwdaccountlockedtime: "Locked since", nsaccountlock: "Account locked",
  jpegphoto: "Photo", userpassword: "Password (hash)", homedirectory: "Home folder", loginshell: "Login shell",
  uidnumber: "User ID number", gidnumber: "Group ID number", entryuuid: "Unique id", entrydn: "DN",
};

// The friendly name of an attribute, or "" when there is none.
export const friendlyLabel = (attr) => LABELS[(attr || "").trim().toLowerCase()] || "";

// "Email (mail)" when we know a friendly name, otherwise just the attribute.
export const labelWithName = (attr) => (friendlyLabel(attr) ? `${friendlyLabel(attr)} (${attr})` : attr);

// Attributes offered as one-click chips, in the order people usually need them. For a
// pair of equivalents (cn / displayName) only the first one the server has is offered.
const CHIP_GROUPS = [
  ["cn", "displayName", "name"],
  ["uid", "sAMAccountName"],
  ["mail"],
  ["sn"],
  ["givenName"],
  ["telephoneNumber", "mobile"],
  ["departmentNumber", "department"],
  ["title"],
  ["objectClass"],
  ["createTimestamp", "whenCreated"],
  ["modifyTimestamp", "whenChanged"],
  ["memberOf"],
];

// The chips for a server: the first existing attribute of each group, at most `max`.
// `has(name)` says whether the schema defines it.
export function commonChips(has, max = 12) {
  const out = [];
  for (const group of CHIP_GROUPS) {
    const attr = group.find((a) => has(a));
    if (attr) out.push({ attr, label: friendlyLabel(attr) || attr });
    if (out.length >= max) break;
  }
  return out;
}

// What a new condition on this attribute starts as, so that one click is usually enough.
export function chipDefaults(attr, info) {
  const kind = info?.kind;
  if (kind === "time") return { op: "withinDays", value: "7" };
  if (attr.toLowerCase() === "objectclass" || kind === "dn" || kind === "boolean" || kind === "integer") {
    return { op: "equals", value: kind === "boolean" ? "true" : "" };
  }
  return { op: "contains", value: "" };
}

// ---------- the "where to look and what to show" summary ----------

export const SCOPE_WORDS = {
  sub: "the whole branch below",
  one: "only the direct children of",
  base: "only",
};

export function optionsSummary({ base, scope, limit, attrs }) {
  const where = (base || "").trim() ? `${SCOPE_WORDS[scope] || SCOPE_WORDS.sub} ${base.trim()}` : "the whole directory";
  const shows = (attrs || "").trim() ? `showing ${attrs.trim().split(/[,\s]+/).filter(Boolean).join(", ")}` : "showing all attributes";
  return `Looking in ${where}, ${shows}, at most ${limit || 500} results`;
}
