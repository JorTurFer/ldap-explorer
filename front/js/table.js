// Pure logic for the results table and CSV export. No DOM access: unit-tested
// with `node --test` (see front/test).
//
// An entry is { dn, attrs: [{ name, values: [string], binary?: boolean }] }, the
// shape the API returns.

// Splits the "Attributes" field ("cn, mail memberOf") into names.
export const parseAttrList = (s) => (s || "").split(/[,\s]+/).filter(Boolean);

export const cellText = (attr) => (attr.values || []).join("; ");

// Decides the table columns (the DN is always shown separately).
//  - Attributes the user asked for come first, in their order and spelling.
//  - With nothing requested, or with "*" / "+", the attributes found in the
//    results follow, most frequent first.
export function collectColumns(entries, requested = []) {
  const explicit = [];
  const seen = new Set();
  let wantAll = requested.length === 0;
  for (const a of requested) {
    if (a === "*" || a === "+") { wantAll = true; continue; }
    if (a === "1.1") continue; // "no attributes"
    const k = a.toLowerCase();
    if (!seen.has(k)) { seen.add(k); explicit.push(a); }
  }
  if (!wantAll) return explicit;

  const count = new Map();
  const spelling = new Map();
  for (const e of entries) {
    for (const a of e.attrs || []) {
      const k = a.name.toLowerCase();
      count.set(k, (count.get(k) || 0) + 1);
      if (!spelling.has(k)) spelling.set(k, a.name);
    }
  }
  const rest = [...count.keys()]
    .filter((k) => !seen.has(k))
    .sort((x, y) => count.get(y) - count.get(x) || x.localeCompare(y))
    .map((k) => spelling.get(k));
  return [...explicit, ...rest];
}

// One row per entry: { dn, cells } with one text cell per column ("" if absent).
export function buildRows(entries, columns) {
  const keys = columns.map((c) => c.toLowerCase());
  return entries.map((e) => {
    const byName = new Map();
    for (const a of e.attrs || []) byName.set(a.name.toLowerCase(), cellText(a));
    return { dn: e.dn, cells: keys.map((k) => byName.get(k) ?? "") };
  });
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

// Returns a new array sorted by a column (index into cells, or -1 for the DN).
// Natural order ("u2" before "u10"), case-insensitive, stable, and empty cells
// always last whatever the direction.
export function sortRows(rows, col, dir = "asc") {
  const sign = dir === "desc" ? -1 : 1;
  const key = (r) => (col < 0 ? r.dn : r.cells[col]);
  return rows
    .map((r, i) => [r, i])
    .sort(([a, i], [b, j]) => {
      const x = key(a), y = key(b);
      if (x === "" && y === "") return i - j;
      if (x === "") return 1;
      if (y === "") return -1;
      return sign * collator.compare(x, y) || i - j;
    })
    .map(([r]) => r);
}

// ---------- CSV ----------

// Numbers and phone numbers ("+34 600 000 001", "-5", "+1 (555) 010-9999").
const NUMBERISH = /^[+-]?[\d\s().-]+$/;

// A multi-valued cell is joined with "; "; it is safe only if every value is number-like.
const numberish = (s) => s.split("; ").every((p) => NUMBERISH.test(p));

// Spreadsheets run cells that start with = + - @ (or tab/CR) as formulas, and a
// directory is untrusted input, so such cells get a leading apostrophe.
// Number-like values (phone numbers) are left alone.
export function csvCell(value) {
  let s = String(value ?? "");
  if (/^[=@\t\r]/.test(s) || (/^[+-]/.test(s) && !numberish(s))) s = "'" + s;
  return /[",\r\n]|^\s|\s$/.test(s) ? '"' + s.replaceAll('"', '""') + '"' : s;
}

// RFC 4180 CSV: a "dn" column, then the given columns. CRLF line endings.
// bom: prefix a UTF-8 byte order mark so Excel detects the encoding.
export function toCSV(columns, rows, { bom = false } = {}) {
  const lines = [["dn", ...columns], ...rows.map((r) => [r.dn, ...r.cells])].map((l) => l.map(csvCell).join(","));
  return (bom ? "﻿" : "") + lines.join("\r\n") + "\r\n";
}

// ---------- JSON ----------

// One entry as a plain object: { dn, attributes: { name: [values] } }. Binary values are
// the summaries the API returns (use the LDIF export for the real bytes).
export function entryToObject(entry) {
  const attributes = {};
  for (const a of entry.attrs || []) attributes[a.name] = [...(a.values || [])];
  return { dn: entry.dn, attributes };
}

export const entryToJSON = (entry) => JSON.stringify(entryToObject(entry), null, 2);
export const entriesToJSON = (entries) => JSON.stringify(entries.map(entryToObject), null, 2);
