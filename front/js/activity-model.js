// What the status bar says about the requests in flight: what each one is, how long it has
// been running, how long it usually takes, whether it is just slow or looks stuck. Pure (no
// DOM, no network, the time is passed in): unit-tested with `node --test`.

import { splitDN } from "./nav.js";

export const SHOW_AFTER_MS = 400;     // faster requests never reach the bar (no flicker)
export const SLOW_AFTER_MS = 10000;   // from here on we say it is taking a while
export const QUIET_AFTER_MS = 30000;  // no sign of life for this long: say so, offer to cancel
export const DONE_SHOWN_MS = 8000;    // how long "Done: 600 entries in 1.8 s" stays
export const HISTORY_LIMIT = 20;      // finished requests kept in the list (this session only)
const EXPECT_MIN_MS = 2000;           // an "usually" under this is not worth showing

const short = (s, n) => (s.length > n ? s.slice(0, n - 1) + "…" : s);
const firstRDN = (dn) => { const p = splitDN(dn || "")[0]; return p ? p.rdn : dn || ""; };

// What a request is, in words, and whether it is worth showing at all. `kind` groups requests
// that take about as long as each other (for the "usually" estimate); `key` is the same
// request again (same base, scope and filter).
export function describeRequest(method, path, body) {
  const [pathname, query = ""] = path.split("?");
  const qp = new URLSearchParams(query);
  const quiet = { quiet: true, label: "", kind: "", key: "" };
  const item = (label, kind, key = "") => ({ quiet: false, label, kind, key: kind + "|" + key });
  switch (pathname) {
    case "/api/children": return item(`Loading the entries under ${short(firstRDN(qp.get("dn")), 50)}`, "children", qp.get("dn") || "");
    case "/api/entry": return item(`Reading ${short(firstRDN(qp.get("dn")), 50)}`, "entry", qp.get("dn") || "");
    case "/api/entry/ldif": return item("Preparing the LDIF of the entry", "entry-ldif", qp.get("dn") || "");
    case "/api/entry/groups": return item(`Looking up the groups of ${short(firstRDN(qp.get("dn")), 40)}`, "groups", qp.get("dn") || "");
    case "/api/entry/value": return item("Fetching the value", "value", qp.get("dn") + "|" + qp.get("attr"));
    case "/api/search":
    case "/api/search/ldif": {
      const b = body || {};
      const what = pathname.endsWith("ldif") ? "Exporting" : "Searching";
      const where = b.base ? ` in ${short(firstRDN(b.base), 40)}` : "";
      const filter = b.filter ? ` for ${short(b.filter, 60)}` : "";
      return item(`${what}${where}${filter}`, "search", [b.base, b.scope, b.filter].join("|"));
    }
    case "/api/schema": return item("Loading the server's schema", "schema");
    case "/api/server": return item("Reading the server's information", "server");
    case "/api/sample": return item(`Looking at the entries in ${short(firstRDN(qp.get("base")), 40)}`, "sample", qp.get("base") || "");
    case "/api/connect": return item("Connecting to the server", "connect", (body && body.url) || "");
    default:
      if (pathname.startsWith("/api/recipes/")) return item("Running the recipe", "recipe", pathname);
      return quiet; // the app's own housekeeping (profiles, settings, heartbeat, the filter builder...)
  }
}

export const formatDuration = (ms) => {
  const s = Math.max(0, ms) / 1000;
  if (s < 10) return `${s.toFixed(1)} s`;
  if (s < 60) return `${Math.round(s)} s`;
  const m = Math.floor(s / 60), r = Math.round(s - m * 60);
  return `${m} min ${String(r).padStart(2, "0")} s`;
};

const plural = (n, one, many) => `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
export const entriesText = (n) => plural(n, "entry", "entries");

// "Done in 1.8 s · 600 entries", "Cancelled after 3.0 s", "Failed after 1.0 s".
export const outcomeText = (h) => h.outcome === "ok"
  ? `Done in ${formatDuration(h.ms)}${h.entries > 0 ? ` · ${entriesText(h.entries)}` : ""}`
  : h.outcome === "cancelled" ? `Cancelled after ${formatDuration(h.ms)}` : `Failed after ${formatDuration(h.ms)}`;

// The wall-clock time a request started, as HH:MM:SS (the browser's own time zone).
export const clockTime = (wallMs) => (wallMs ? new Date(wallMs).toLocaleTimeString("en-GB", { hour12: false }) : "");

const median = (xs) => { const s = [...xs].sort((a, b) => a - b); return s[Math.floor((s.length - 1) / 2)]; };

export function createTracker() {
  const running = new Map();   // id -> { label, kind, key, startedAt, entries, pages, quietMs, seenAt }
  const byKey = new Map();     // key -> last duration (ms) of a successful request
  const byKind = new Map();    // kind -> last few durations
  let last = null;             // { label, ms, entries, at, outcome }
  let history = [];            // the latest finished requests, newest first

  const expectedFor = (r) => {
    const exact = byKey.get(r.key);
    const kind = byKind.get(r.kind);
    const e = exact ?? (kind && kind.length >= 2 ? median(kind) : null);
    return e != null && e >= EXPECT_MIN_MS ? e : null;
  };

  // What to say about one running request.
  const detail = (r, now) => {
    const elapsed = now - r.startedAt;
    const expected = expectedFor(r);
    const sinceSign = Math.max(now - r.seenAt, r.quietMs);
    const stuck = sinceSign >= QUIET_AFTER_MS;
    const slow = elapsed >= SLOW_AFTER_MS || (expected != null && elapsed > expected * 1.5);
    const parts = [formatDuration(elapsed)];
    if (r.entries > 0) parts.push(`${entriesText(r.entries)} so far`);
    if (expected != null) parts.push(`usually about ${formatDuration(expected)}`);
    let note = "";
    if (stuck) note = `No answer from the server for ${formatDuration(sinceSign)}. It may just be slow, not stopped; you can wait or cancel.`;
    else if (slow) note = "Taking longer than usual. Big directories can be slow; it is still working.";
    return {
      id: r.id, label: r.label, elapsed, parts, note,
      level: stuck ? "stuck" : slow ? "slow" : "ok",
      // a bar that fills only when we know what to expect; otherwise it just moves
      fraction: expected != null ? Math.min(0.95, elapsed / expected) : null,
    };
  };

  return {
    start(id, desc, now, wall = 0) {
      if (desc.quiet) return;
      running.set(id, { ...desc, id, startedAt: now, wall, entries: 0, pages: 0, quietMs: 0, seenAt: now });
    },
    // What the server says about a request (counters of a paged search).
    progress(id, p, now) {
      const r = running.get(id);
      if (!r) return;
      if (p.entries !== r.entries || p.pages !== r.pages) r.seenAt = now;
      r.entries = p.entries; r.pages = p.pages; r.quietMs = p.quietMs ?? 0;
    },
    finish(id, now, outcome = "ok") {
      const r = running.get(id);
      if (!r) return;
      running.delete(id);
      const ms = now - r.startedAt;
      if (outcome === "ok") {
        byKey.set(r.key, ms);
        const list = byKind.get(r.kind) || [];
        byKind.set(r.kind, [...list, ms].slice(-5));
      }
      // The bar announces only what took a noticeable time (no flicker); the list has everything.
      if (ms >= SHOW_AFTER_MS || outcome !== "ok") last = { label: r.label, ms, entries: r.entries, at: now, outcome };
      history = [{ label: r.label, startedWall: r.wall, ms, entries: r.entries, outcome }, ...history].slice(0, HISTORY_LIMIT);
    },
    ids: () => [...running.keys()],
    // Everything for the expanded list: every request that is running (oldest first) and the
    // latest finished ones (newest first), however short they were.
    list(now) {
      const running_ = [...running.values()]
        .sort((a, b) => a.startedAt - b.startedAt).map((r) => detail(r, now));
      return { running: running_, history: history.map((h) => ({ ...h, text: outcomeText(h) })) };
    },
    clearHistory() { history = []; last = null; },
    // What to draw now. `busy` is false (nothing to show) until a request has been running for
    // SHOW_AFTER_MS; `done` describes the last finished one for a few seconds.
    view(now) {
      const shown = [...running.values()].filter((r) => now - r.startedAt >= SHOW_AFTER_MS);
      if (shown.length) {
        const r = shown.reduce((a, b) => (a.startedAt <= b.startedAt ? a : b)); // the oldest
        return { ...detail(r, now), busy: true, others: shown.length - 1, done: null };
      }
      if (last && now - last.at < DONE_SHOWN_MS) {
        const text = outcomeText(last);
        return { busy: false, level: last.outcome === "ok" ? "ok" : last.outcome === "cancelled" ? "slow" : "stuck", done: { label: last.label, text }, parts: [], note: "", others: 0, fraction: null };
      }
      return { busy: false, done: null, parts: [], note: "", level: "ok", others: 0, fraction: null };
    },
  };
}
