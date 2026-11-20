// Access to the local JSON API. The token lives in the URL fragment, which the
// browser never sends to the server; it goes in a header instead.

const TOKEN = location.hash.slice(1);
history.replaceState(null, "", location.pathname); // remove the token from the address bar

import { beginRequest } from "./activity-core.js";

// Every call goes through here, so the status bar knows what is in flight, for how long, and
// can cancel it. X-Op tags the request so the server can report its progress.
async function send(method, path, body, read, extraHeaders = {}) {
  const t = beginRequest(method, path, body);
  const headers = { "X-Token": TOKEN, ...extraHeaders };
  if (!t.quiet) headers["X-Op"] = t.id;
  try {
    const r = await fetch(path, { method, headers, body: body ? JSON.stringify(body) : undefined, signal: t.signal });
    const out = await read(r);
    t.end("ok");
    return out;
  } catch (e) {
    t.end("error");
    if (e.name === "AbortError" || t.wasCancelled()) { const c = new Error("Cancelled"); c.cancelled = true; throw c; }
    throw e;
  }
}

const json = { "Content-Type": "application/json" };

export function api(path, opts = {}) {
  return send(opts.method || (opts.body ? "POST" : "GET"), path, opts.body, async (r) => {
    let data; try { data = await r.json(); } catch { data = {}; }
    if (!r.ok) {
      const err = new Error(data.error || r.statusText);
      err.code = data.code;   // e.g. "vault_locked": the UI reacts to it
      err.status = r.status;
      throw err;
    }
    return data;
  }, json);
}

// Like api() but for endpoints that answer with plain text (LDIF): returns the text, the
// file name the server suggests and any warning (a partial export).
export function apiText(path, opts = {}) {
  return send(opts.method || (opts.body ? "POST" : "GET"), path, opts.body, async (r) => {
    if (!r.ok) {
      let data = {}; try { data = await r.json(); } catch { /* not JSON */ }
      throw new Error(data.error || r.statusText);
    }
    const m = /filename="([^"]+)"/.exec(r.headers.get("Content-Disposition") || "");
    return { text: await r.text(), filename: m ? m[1] : "export.txt", warning: r.headers.get("X-Warning") || "" };
  }, json);
}

// Raw bytes of one attribute value (/api/entry/value): { bytes, type, filename }.
export function apiBytes(path) {
  return send("GET", path, undefined, async (r) => {
    if (!r.ok) {
      let data = {}; try { data = await r.json(); } catch { /* not JSON */ }
      throw new Error(data.error || r.statusText);
    }
    const m = /filename="([^"]+)"/.exec(r.headers.get("Content-Disposition") || "");
    return { bytes: new Uint8Array(await r.arrayBuffer()), type: r.headers.get("Content-Type") || "application/octet-stream", filename: m ? m[1] : "value.bin" };
  });
}
