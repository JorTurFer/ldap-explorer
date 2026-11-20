// Bookmarks: entries you want to come back to, per connection profile. A star in the entry
// view toggles one; the list sits above the tree.
import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { goTo } from "./tree.js";
import { copyBtn } from "./ui/copy.js";
import { toast } from "./ui/toast.js";
import { normDN } from "./nav.js";

let items = [];

export const isBookmarked = (dn) => items.some((b) => normDN(b.dn) === normDN(dn));

export function initBookmarks() {
  on("connected", async () => { await load(); });
  on("disconnected", () => { items = []; render(); });
}

async function load() {
  try { items = await api("/api/bookmarks"); } catch { items = []; }
  render();
}

export async function toggleBookmark(dn) {
  try {
    items = isBookmarked(dn)
      ? await api("/api/bookmarks?dn=" + encodeURIComponent(dn), { method: "DELETE" })
      : await api("/api/bookmarks", { body: { dn } });
    toast(isBookmarked(dn) ? "Bookmarked" : "Bookmark removed");
  } catch (e) { toast(e.message, "err"); }
  render();
}

function render() {
  $("bmCount").textContent = String(items.length);
  $("bmEmpty").classList.toggle("hidden", items.length > 0);
  $("bmList").replaceChildren(...items.map((b) => el("li", { class: "bm", "data-dn": b.dn },
    el("a", { class: "dnlink", textContent: b.label, title: b.dn, onclick: () => goTo(b.dn) }),
    copyBtn(b.dn, { title: "Copy the DN", what: "Copied the DN" }),
    el("button", { type: "button", class: "copy-btn bm-remove", textContent: "×", title: "Remove this bookmark", "aria-label": "Remove this bookmark",
      onclick: () => toggleBookmark(b.dn) }))));
  emit("bookmarks-changed", items);
}
