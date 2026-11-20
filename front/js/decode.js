// Turning raw LDAP values into something a person can read. Pure: unit-tested with `node --test`.

// LDAP GeneralizedTime: YYYYMMDDHHMMSS[.fff][Z | +hh[mm] | -hh[mm]] (minutes and seconds
// are optional). Returns a Date, or null when the text is not one.
export function parseGeneralizedTime(s) {
  const m = /^(\d{4})(\d{2})(\d{2})(\d{2})?(\d{2})?(\d{2})?(?:[.,](\d+))?(Z|[+-]\d{2}(?:\d{2})?)?$/.exec((s || "").trim());
  if (!m) return null;
  const [, y, mo, d, h = "00", mi = "00", sec = "00", frac = "", zone = "Z"] = m;
  if (+mo < 1 || +mo > 12 || +d < 1 || +d > 31 || +h > 23 || +mi > 59 || +sec > 60) return null;
  let t = Date.UTC(+y, +mo - 1, +d, +h, +mi, +sec, frac ? Math.round(parseFloat("0." + frac) * 1000) : 0);
  const back = new Date(t);
  if (back.getUTCMonth() !== +mo - 1) return null; // 31 February and friends
  if (zone !== "Z") {
    const sign = zone[0] === "-" ? -1 : 1;
    const hh = +zone.slice(1, 3), mm = zone.length > 3 ? +zone.slice(3, 5) : 0;
    t -= sign * (hh * 60 + mm) * 60000; // "+02:00" is two hours ahead of UTC
  }
  return new Date(t);
}

const UNITS = [["year", 365 * 24 * 3600], ["month", 30 * 24 * 3600], ["day", 24 * 3600], ["hour", 3600], ["minute", 60]];

// "3 days ago", "in 2 hours", "just now".
export function relativeTime(date, now = new Date()) {
  const secs = Math.round((date - now) / 1000);
  const abs = Math.abs(secs);
  if (abs < 45) return "just now";
  for (const [name, size] of UNITS) {
    if (abs >= size * 0.9 || name === "minute") {
      const n = Math.max(1, Math.round(abs / size));
      const text = `${n} ${name}${n === 1 ? "" : "s"}`;
      return secs < 0 ? `${text} ago` : `in ${text}`;
    }
  }
  return "just now";
}

const pad = (n) => String(n).padStart(2, "0");
export const formatUTC = (d) => `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())} UTC`;

// "2026-11-19 12:00:00 UTC · 3 days ago", or "" if the value is not a GeneralizedTime.
export function describeTime(value, now = new Date()) {
  const d = parseGeneralizedTime(value);
  return d ? `${formatUTC(d)} · ${relativeTime(d, now)}` : "";
}

// A byte count for people: 1.5 KB.
export function formatBytes(n) {
  if (n < 1024) return `${n} bytes`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// The size in a "(binary, N bytes) hex" summary from the API, or null.
export function summaryBytes(summary) {
  const m = /^\(binary, (\d+) bytes\)/.exec(summary || "");
  return m ? +m[1] : null;
}

// Attributes that hold a picture people want to see.
export const isPhotoAttr = (name) => ["jpegphoto", "thumbnailphoto", "photo"].includes((name || "").toLowerCase());

// btoa for bytes, in chunks so big values do not overflow the call stack.
export function bytesToBase64(bytes) {
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(bin);
}
