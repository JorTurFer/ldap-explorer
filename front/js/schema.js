// The connected server's schema, loaded once per connection, used to suggest
// attributes and to choose sensible operators and inputs.

import { api } from "./api.js";
import { on } from "./events.js";

let loading = null; // Promise<index | null>

on("connected", () => { loading = null; });
on("disconnected", () => { loading = null; });

// Resolves to the schema index, or null when the server does not publish one
// (the builder then works without suggestions).
export function getSchema() {
  if (!loading) {
    loading = api("/api/schema").then(buildIndex).catch(() => null);
  }
  return loading;
}

function buildIndex(raw) {
  const attrs = new Map();
  for (const a of raw.attributes) {
    attrs.set(a.oid.toLowerCase(), a);
    for (const n of a.names) attrs.set(n.toLowerCase(), a);
  }
  const usable = raw.attributes.filter((a) => !a.obsolete);
  // user attributes first (alphabetical), then operational ones
  const names = [...usable.filter((a) => !a.operational), ...usable.filter((a) => a.operational)].map((a) => a.name);
  const classes = raw.objectClasses.filter((c) => !c.obsolete).map((c) => c.name);
  return { attrs, names, classes, errors: raw.errors || [] };
}

export const attrInfo = (idx, name) => (idx ? idx.attrs.get((name || "").trim().toLowerCase()) || null : null);
