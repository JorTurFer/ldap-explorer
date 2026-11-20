// Copying to the clipboard, everywhere the same way: a quiet "copy" button next to the
// thing it copies, and a toast that says what was copied.
import { el } from "../dom.js";
import { toast } from "./toast.js";

// Puts text in the clipboard. navigator.clipboard needs a secure context (127.0.0.1 is
// one) and a user gesture; the textarea fallback covers the rest.
export async function copyText(text, what = "Copied") {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = el("textarea", { value: text });
    ta.setAttribute("readonly", "");
    ta.className = "copy-scratch";
    document.body.append(ta);
    ta.select();
    try { document.execCommand("copy"); } finally { ta.remove(); }
  }
  toast(what);
}

// A "copy" button. `what` is the confirmation ("Copied mail"); title is the tooltip.
export function copyBtn(text, { label = "copy", what = "Copied", title = "Copy to the clipboard" } = {}) {
  return el("button", { type: "button", class: "copy-btn", textContent: label, title, "aria-label": title,
    onclick: (ev) => { ev.stopPropagation(); ev.preventDefault(); copyText(text, what); } });
}

// Wraps a node so a copy button appears next to it when you point at it or tab to it.
export function withCopy(node, text, opts) {
  return el("span", { class: "has-copy" }, node, copyBtn(text, opts));
}

// Truncates what a toast says, so copying a long value does not flood the screen.
export const preview = (s, n = 40) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

// A copy button whose text is only known after asking the server (a binary value as base64).
export function asyncCopyBtn(fn, { label, what = "Copied", title = "Copy to the clipboard" }) {
  return el("button", { type: "button", class: "copy-btn", textContent: label, title, "aria-label": title,
    onclick: async (ev) => {
      ev.stopPropagation(); ev.preventDefault();
      try { copyText(await fn(), what); } catch (e) { toast(e.message, "err"); }
    } });
}
