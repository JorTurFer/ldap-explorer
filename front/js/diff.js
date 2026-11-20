// Comparing two entries attribute by attribute. Pure: unit-tested with `node --test`.
//
// An entry is { dn, attrs: [{ name, values: [string] }] } (what /api/entry returns).

const lower = (s) => (s || "").toLowerCase();

// One row per attribute found in either entry:
//   { name, status: "same" | "different" | "onlyA" | "onlyB", a: [values], b: [values],
//     removed: [values only in A], added: [values only in B] }
// objectClass first, then alphabetical. Values are compared as sets (their order is not
// meaningful in LDAP) and exactly (case matters, as the server stores them).
export function diffEntries(a, b) {
  const mapOf = (e) => {
    const m = new Map();
    for (const at of e.attrs || []) m.set(lower(at.name), { name: at.name, values: [...(at.values || [])] });
    return m;
  };
  const ma = mapOf(a), mb = mapOf(b);
  const keys = [...new Set([...ma.keys(), ...mb.keys()])];
  const rows = keys.map((k) => {
    const x = ma.get(k), y = mb.get(k);
    const av = x ? x.values : [], bv = y ? y.values : [];
    const removed = av.filter((v) => !bv.includes(v));
    const added = bv.filter((v) => !av.includes(v));
    let status = "same";
    if (!x) status = "onlyB";
    else if (!y) status = "onlyA";
    else if (removed.length || added.length) status = "different";
    return { name: (x || y).name, status, a: av, b: bv, removed, added };
  });
  return rows.sort((p, q) => {
    const po = lower(p.name) === "objectclass", qo = lower(q.name) === "objectclass";
    if (po !== qo) return po ? -1 : 1;
    return lower(p.name).localeCompare(lower(q.name));
  });
}

export function summarize(rows) {
  const s = { same: 0, different: 0, onlyA: 0, onlyB: 0 };
  for (const r of rows) s[r.status]++;
  return s;
}

// "2 differ, 1 only on the left, 3 only on the right, 5 identical"
export function summaryText(s, nameA = "the first", nameB = "the second") {
  const parts = [];
  if (s.different) parts.push(`${s.different} differ`);
  if (s.onlyA) parts.push(`${s.onlyA} only in ${nameA}`);
  if (s.onlyB) parts.push(`${s.onlyB} only in ${nameB}`);
  if (s.same) parts.push(`${s.same} identical`);
  return parts.length ? parts.join(", ") : "Nothing to compare";
}

export const isIdentical = (rows) => rows.every((r) => r.status === "same");
