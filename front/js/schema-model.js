// Pure logic of the schema browser and the server panel: no DOM. Unit-tested with `node --test`.
//
// An attribute is { name, names[], oid, desc, ... }; an object class is
// { name, names[], oid, desc, kind, sup[], must[], may[] } (what /api/schema returns).

const lower = (s) => (s || "").toLowerCase();

// Items matching every word of the text in their name, aliases, OID or description.
// Best matches first (exact name, then names starting with the text, then the rest),
// each group alphabetical.
export function filterItems(items, text) {
  const words = lower(text).split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...items].sort((a, b) => lower(a.name).localeCompare(lower(b.name)));
  const first = words[0];
  const rank = (it) => {
    const names = [it.name, ...(it.names || [])].map(lower);
    if (names.includes(first)) return 0;
    if (names.some((n) => n.startsWith(first))) return 1;
    return 2;
  };
  return items
    .filter((it) => {
      const hay = [it.name, ...(it.names || []), it.oid, it.desc].map(lower).join(" ");
      return words.every((w) => hay.includes(w));
    })
    .sort((a, b) => rank(a) - rank(b) || lower(a.name).localeCompare(lower(b.name)));
}

// name, alias or OID -> item
export function indexByName(items) {
  const m = new Map();
  for (const it of items) {
    m.set(lower(it.oid), it);
    for (const n of (it.names && it.names.length ? it.names : [it.name])) m.set(lower(n), it);
  }
  return m;
}

// The class, its parents, their parents...: each class once, nearest first.
export function classChain(classes, name) {
  const idx = indexByName(classes);
  const out = [], seen = new Set();
  const visit = (n) => {
    const c = idx.get(lower(n));
    if (!c || seen.has(c)) return;
    seen.add(c);
    out.push(c);
    for (const p of c.sup || []) visit(p);
  };
  visit(name);
  return out;
}

// Every attribute an entry of this class may have, including what the parents add.
// Required ones first, then optional; each attribute once (required wins), alphabetical.
export function effectiveAttrs(classes, name) {
  const required = new Map(), optional = new Map();
  for (const c of classChain(classes, name)) {
    for (const a of c.must || []) required.set(lower(a), { name: a, required: true, from: c.name });
    for (const a of c.may || []) if (!optional.has(lower(a))) optional.set(lower(a), { name: a, required: false, from: c.name });
  }
  for (const k of required.keys()) optional.delete(k);
  const byName = (x, y) => lower(x.name).localeCompare(lower(y.name));
  return [...[...required.values()].sort(byName), ...[...optional.values()].sort(byName)];
}

// The classes that list the attribute themselves as required or optional (not through a parent).
export function usedBy(classes, attr) {
  const a = lower(attr);
  return classes
    .map((c) => ({ class: c.name, required: (c.must || []).some((m) => lower(m) === a), optional: (c.may || []).some((m) => lower(m) === a) }))
    .filter((u) => u.required || u.optional)
    .sort((x, y) => Number(y.required) - Number(x.required) || lower(x.class).localeCompare(lower(y.class)));
}

export const KIND_TEXT = {
  string: "text", integer: "a whole number", boolean: "yes / no", time: "a date and time",
  dn: "the DN of another entry", binary: "binary data (a file)", other: "other",
};

export const CLASS_KIND_TEXT = {
  STRUCTURAL: "Structural: what an entry fundamentally is. Every entry has exactly one chain of structural classes.",
  AUXILIARY: "Auxiliary: extra attributes that can be added to an entry of another class.",
  ABSTRACT: "Abstract: only a base for other classes (like top); entries are not created from it.",
};

export const usageText = (usage) => (!usage || usage.toLowerCase() === "userapplications" ? "an ordinary attribute" : `operational (${usage}): kept by the server, shown only when you ask for operational attributes`);

// ---------- the server panel ----------

export const FLAVORS = { openldap: "OpenLDAP", "389ds": "389 Directory Server", ad: "Active Directory", unknown: "Unknown (generic LDAP v3 server)" };
export const flavorLabel = (f) => FLAVORS[f] || FLAVORS.unknown;

const CONTROLS = {
  "1.2.840.113556.1.4.319": "Paged results",
  "1.2.840.113556.1.4.473": "Server-side sorting",
  "2.16.840.1.113730.3.4.9": "Virtual list view",
  "1.3.6.1.4.1.4203.1.9.1.1": "Content synchronization (syncrepl)",
  "1.3.6.1.4.1.42.2.27.8.5.1": "Password policy",
  "1.3.6.1.1.12": "Assertion",
  "1.3.6.1.1.13.1": "Pre-read",
  "1.3.6.1.1.13.2": "Post-read",
  "2.16.840.1.113730.3.4.2": "ManageDsaIT",
  "1.3.6.1.4.1.4203.666.5.14": "Subentries",
  "1.2.840.113556.1.4.801": "Security descriptor flags (AD)",
  "2.16.840.1.113730.3.4.18": "Proxied authorization",
  "1.3.6.1.4.1.4203.1.10.1": "Subentries",
  "1.3.6.1.1.22": "Do not use a copy (replica)",
  "1.2.826.0.1.3344810.2.3": "Matched values",
};
const EXTENSIONS = {
  "1.3.6.1.4.1.1466.20037": "StartTLS",
  "1.3.6.1.4.1.4203.1.11.1": "Password change",
  "1.3.6.1.4.1.4203.1.11.3": "Who am I?",
  "1.3.6.1.4.1.1466.101.119.1": "Refresh dynamic entries",
  "1.3.6.1.1.8": "Cancel",
  "1.3.6.1.1.21.1": "Start transaction",
  "1.3.6.1.1.21.3": "End transaction",
};

// { name, oid }: the name is "" when the OID is not one we know.
export const controlInfo = (oid) => ({ name: CONTROLS[oid] || "", oid });
export const extensionInfo = (oid) => ({ name: EXTENSIONS[oid] || "", oid });

// "123 attributes", or "showing 300 of 412 attributes: type to narrow it down" when only the
// first `max` are listed. It always says what is being counted.
export function formatCount(n, max, what) {
  return n > max ? `showing ${max} of ${n} ${what}: type to narrow it down` : `${n} ${what}`;
}
