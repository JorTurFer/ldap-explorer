// Access to the local JSON API. The token lives in the URL fragment, which the
// browser never sends to the server; it goes in a header instead.

const TOKEN = location.hash.slice(1);
history.replaceState(null, "", location.pathname); // remove the token from the address bar

export async function api(path, opts = {}) {
  const r = await fetch(path, {
    method: opts.method || (opts.body ? "POST" : "GET"),
    headers: { "X-Token": TOKEN, "Content-Type": "application/json" },
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  let data; try { data = await r.json(); } catch { data = {}; }
  if (!r.ok) {
    const err = new Error(data.error || r.statusText);
    err.code = data.code;   // e.g. "vault_locked": the UI reacts to it
    err.status = r.status;
    throw err;
  }
  return data;
}

// Like api() but for endpoints that answer with plain text (LDIF): returns the text, the
// file name the server suggests and any warning (a partial export).
export async function apiText(path, opts = {}) {
  const r = await fetch(path, {
    method: opts.method || (opts.body ? "POST" : "GET"),
    headers: { "X-Token": TOKEN, "Content-Type": "application/json" },
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  if (!r.ok) {
    let data = {}; try { data = await r.json(); } catch { /* not JSON */ }
    throw new Error(data.error || r.statusText);
  }
  const m = /filename="([^"]+)"/.exec(r.headers.get("Content-Disposition") || "");
  return { text: await r.text(), filename: m ? m[1] : "export.txt", warning: r.headers.get("X-Warning") || "" };
}

// Raw bytes of one attribute value (/api/entry/value): { bytes, type, filename }.
export async function apiBytes(path) {
  const r = await fetch(path, { headers: { "X-Token": TOKEN } });
  if (!r.ok) {
    let data = {}; try { data = await r.json(); } catch { /* not JSON */ }
    throw new Error(data.error || r.statusText);
  }
  const m = /filename="([^"]+)"/.exec(r.headers.get("Content-Disposition") || "");
  return { bytes: new Uint8Array(await r.arrayBuffer()), type: r.headers.get("Content-Type") || "application/octet-stream", filename: m ? m[1] : "value.bin" };
}
