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
