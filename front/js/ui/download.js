// Saving a text file from the browser. Nothing is uploaded anywhere: the text is turned
// into a Blob and the browser saves it, as if it had been downloaded.
import { el } from "../dom.js";

export function downloadText(filename, text, { mime = "text/plain;charset=utf-8", bom = false } = {}) {
  const url = URL.createObjectURL(new Blob([bom ? "\uFEFF" + text : text], { type: mime }));
  const a = el("a", { href: url, download: filename });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
