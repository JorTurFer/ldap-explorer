// Pure helpers for navigation: splitting and normalising DNs, and the tree
// filter's matching. No DOM access: unit-tested with `node --test`.

// Splits a DN into its RDNs, honouring backslash escapes (`\,` `\\` `\2C`) and
// quoted values. Each part keeps its position in the original string so callers
// can slice the text exactly as the server returned it.
export function splitDN(dn) {
  const parts = [];
  let start = 0, quoted = false;
  for (let i = 0; i < dn.length; i++) {
    const c = dn[i];
    if (c === "\\") { i++; continue; }          // the next character is literal
    if (c === '"') { quoted = !quoted; continue; }
    if (!quoted && (c === "," || c === ";")) {
      parts.push({ rdn: dn.slice(start, i), start, end: i });
      start = i + 1;
    }
  }
  parts.push({ rdn: dn.slice(start), start, end: dn.length });
  // leading spaces after a separator belong to the separator, not to the RDN
  return parts.map((p) => {
    const lead = p.rdn.length - p.rdn.trimStart().length;
    const rdn = p.rdn.trim();
    return { rdn, start: p.start + lead, end: p.start + lead + rdn.length };
  });
}

// The DN itself and every ancestor, topmost first: "a=1,b=2,c=3" gives
// ["c=3", "b=2,c=3", "a=1,b=2,c=3"]. Slices keep the original text.
export function ancestors(dn) {
  if (!dn || !dn.trim()) return [];
  const parts = splitDN(dn);
  const out = [];
  for (let i = parts.length - 1; i >= 0; i--) out.push(dn.slice(parts[i].start).trim());
  return out;
}

// The DN of the parent entry, or "" for a top-level entry.
export function parentDN(dn) {
  const parts = splitDN(dn);
  return parts.length < 2 ? "" : dn.slice(parts[1].start).trim();
}

// A canonical form to compare DNs: case-insensitive, no spaces around = + or ,.
export function normDN(dn) {
  return splitDN(dn || "")
    .map((p) => p.rdn.replace(/\s*([=+])\s*/g, "$1").toLowerCase())
    .join(",");
}

// Does the text look like a DN a user typed to jump to an entry?
export const looksLikeDN = (s) => /^[^=,]+=[^,]+(,\s*[^=,]+=[^,]+)*$/.test((s || "").trim());

// Tree filter: every word typed must appear (case-insensitive) in the DN.
export function matchesFilter(dn, text) {
  const words = (text || "").toLowerCase().split(/\s+/).filter(Boolean);
  const hay = (dn || "").toLowerCase();
  return words.every((w) => hay.includes(w));
}
