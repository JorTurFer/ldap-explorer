import { $, el } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { emit, on } from "./events.js";
import { ancestors, normDN, looksLikeDN, matchesFilter } from "./nav.js";
import { copyBtn } from "./ui/copy.js";
import { childrenURL, loadedText, isTimeout } from "./tree-model.js";
import { friendlyError } from "./friendly-errors.js";
import { childHints } from "./settings.js";

let roots = [];
let selectedNode = null;
const lightDNs = new Set(); // branches that were too slow with the full listing: loaded as names only from now on
const nodes = new Map(); // normalised DN -> { open(), select() } for every node drawn so far

export function initTree() {
  on("connected", ({ roots: rs }) => {
    roots = rs;
    $("btnRefresh").disabled = false;
    $("treeFilter").disabled = false;
    buildTree();
  });
  on("disconnected", () => {
    roots = []; selectedNode = null; nodes.clear(); lightDNs.clear();
    $("btnRefresh").disabled = true;
    $("treeFilter").disabled = true; $("treeFilter").value = "";
    $("treeHint").classList.add("hidden");
    $("tree").classList.remove("filtering");
    $("tree").replaceChildren(el("div", { class: "empty", textContent: "Not connected." }));
  });
  $("btnRefresh").onclick = buildTree;
  on("child-hints-changed", () => { if (roots.length) { lightDNs.clear(); buildTree(); } }); // what each node knows changes: start again

  const filter = $("treeFilter");
  filter.addEventListener("input", applyFilter);
  filter.addEventListener("keydown", async (ev) => {
    if (ev.key === "Escape") { filter.value = ""; applyFilter(); return; }
    if (ev.key === "Enter" && looksLikeDN(filter.value)) {
      const dn = filter.value.trim();
      filter.value = ""; applyFilter();   // show the whole tree so the entry can be revealed
      await goTo(dn);
    }
  });
}

function buildTree() {
  nodes.clear();
  selectedNode = null;
  const ul = el("ul", { class: "t" });
  if (!roots.length) {
    $("tree").replaceChildren(el("div", { class: "empty", textContent: "No base was detected. Set a Base DN in the connection." }));
    return;
  }
  for (const r of roots) ul.append(makeNode({ dn: r, rdn: r, leaf: false }));
  $("tree").replaceChildren(ul);
  const first = ul.querySelector(".node"); if (first) first.click();
}

function makeNode(n) {
  const li = el("li");
  const tw = el("span", { class: "tw", textContent: n.leaf ? "" : "▶" });
  const row = el("div", { class: "node", title: n.dn }, tw, el("span", { class: "rdn", textContent: n.rdn }),
    copyBtn(n.dn, { title: "Copy this DN", what: "Copied the DN" }));
  let kids = null, open = false;
  let cursor = "", loaded = 0, moreRow = null, loading = null, observer = null;
  let namesOnly = false; // the listing of this branch asks for the names only (so "Load names only" would change nothing)

  // The "N loaded, more below" row at the end of a long branch. It loads the next page by
  // itself when it scrolls into view (and not while the filter is on: that would read the
  // whole branch just to look for a match), and has buttons for doing it by hand.
  const showMore = (failure = "", timedOut = false) => {
    observer?.disconnect();
    if (!cursor && !failure) { moreRow?.remove(); moreRow = null; return; }
    const row = el("li", { class: "more more-row" });
    if (failure) {
      row.append(el("span", { class: "err", textContent: failure }),
        el("button", { type: "button", class: "small-btn more-retry", textContent: timedOut ? "Try again" : "Reload this branch", onclick: () => reload(false) }),
        ...(timedOut && !namesOnly ? [el("button", { type: "button", class: "small-btn more-light", textContent: "Load names only",
          title: "Ask the server for the names only, which is much cheaper on a huge branch. Every entry then shows the arrow, because it is not asked which ones have children.",
          onclick: () => reload(true) })] : []));
    } else {
      row.append(el("span", { class: "muted", textContent: loadedText(loaded) }),
        el("button", { type: "button", class: "small-btn more-next", textContent: "Load more", onclick: () => loadMore() }),
        el("button", { type: "button", class: "small-btn more-all", textContent: "Load all", title: "Keep loading until the whole branch is here (can take a while on a big one)", onclick: loadAll }));
    }
    if (moreRow) moreRow.replaceWith(row); else kids.append(row);
    moreRow = row;
    if (!failure && "IntersectionObserver" in window) {
      observer = new IntersectionObserver((hits) => {
        if (hits.some((h) => h.isIntersecting) && !$("tree").classList.contains("filtering")) loadMore();
      }, { root: $("tree"), rootMargin: "120px" });
      observer.observe(row);
    }
  };

  const addPage = (res) => {
    for (const c of res.children) {
      const child = makeNode(c);
      if (moreRow) moreRow.before(child); else kids.append(child);
    }
    loaded += res.children.length;
    cursor = res.cursor || "";
    if (res.warning) kids.append(el("li", { class: "more", textContent: "⚠ " + res.warning }));
    showMore();
    applyFilter(); // children that arrive while a filter is active are filtered too
  };

  // Loads the next page. Resolves to false when it could not (the error is shown in the row).
  const loadMore = () => {
    if (!cursor) return Promise.resolve(false);
    if (loading) return loading;
    moreRow?.classList.add("loading");
    loading = (async () => {
      try { addPage(await api(childrenURL(n.dn, cursor))); return true; }
      catch (e) {
        cursor = "";
        showMore(e.cancelled ? "Cancelled. The rest of this branch is not loaded." : friendlyError(e.message), !e.cancelled && isTimeout(e.message));
        return false;
      } finally { loading = null; }
    })();
    return loading;
  };
  const loadAll = async () => { while (cursor) if (!(await loadMore())) break; };
  const reload = (light) => {
    if (light) lightDNs.add(normDN(n.dn));
    kids?.remove(); kids = null; moreRow = null; cursor = ""; loaded = 0; open = false; toggle();
  };

  const toggle = async () => {
    if (n.leaf) return;
    open = !open;
    if (open && !kids) {
      tw.textContent = "…";
      try {
        const slow = lightDNs.has(normDN(n.dn));           // this branch already timed out with the full listing
        namesOnly = slow || !childHints();
        const res = await api(childrenURL(n.dn, "", namesOnly));
        kids = el("ul", { class: "t" });
        if (!res.children.length) { n.leaf = true; tw.textContent = ""; return; }
        li.append(kids);
        if (slow) kids.append(el("li", { class: "more", textContent: "Names only (the server was slow): every entry shows ▶." }));
        addPage(res);
      } catch (e) {
        if (e.cancelled) { open = false; tw.textContent = "▶"; return; }
        // the error stays where the entries would be, with what can be done about it
        kids = el("ul", { class: "t" });
        li.append(kids);
        showMore(friendlyError(e.message), isTimeout(e.message));
      }
    }
    if (kids) kids.classList.toggle("collapsed", !open);
    tw.textContent = open ? "▼" : "▶";
  };
  tw.addEventListener("click", (ev) => { ev.stopPropagation(); toggle(); });
  const select = () => {
    if (selectedNode) selectedNode.classList.remove("sel");
    row.classList.add("sel"); selectedNode = row;
    emit("select", { dn: n.dn });
  };
  row.addEventListener("click", select);
  row.addEventListener("dblclick", toggle);
  nodes.set(normDN(n.dn), {
    open: async () => { if (!n.leaf && !open) await toggle(); },
    // keep loading pages of this branch until the wanted child is there (or there is no more)
    loadUntil: async (target) => { while (cursor && !nodes.has(target)) if (!(await loadMore())) break; },
    select: () => { select(); row.scrollIntoView({ block: "nearest" }); },
  });
  li.append(row);
  return li;
}

// ---------- quick filter ----------

function applyFilter() {
  const text = $("treeFilter").value;
  const active = text.trim() !== "";
  $("treeHint").classList.toggle("hidden", !active);
  $("tree").classList.toggle("filtering", active);
  // A node shows if it matches or has a visible descendant.
  const walk = (ul) => {
    let any = false;
    for (const li of ul.children) {
      const row = li.querySelector(":scope > .node");
      if (!row) { any = any || !active; continue; }      // the "⚠ more" line
      const sub = li.querySelector(":scope > ul");
      const childShows = sub ? walk(sub) : false;
      const show = !active || matchesFilter(row.title, text) || childShows;
      li.classList.toggle("hidden", !show);
      any = any || show;
    }
    return any;
  };
  const top = $("tree").querySelector(":scope > ul.t");
  if (top) walk(top);
}

// ---------- go to a DN ----------

// Selects an entry in the tree, opening the branches on the way (loading them
// as needed). If it is outside what the tree can reach, just shows the entry.
export async function goTo(dn) {
  const chain = ancestors(dn);
  const first = chain.findIndex((a) => nodes.has(normDN(a)));
  if (first >= 0) {
    for (let i = first; i < chain.length; i++) {
      const node = nodes.get(normDN(chain[i]));
      if (!node) break;
      if (i === chain.length - 1) { node.select(); return; }
      await node.open();
      await node.loadUntil(normDN(chain[i + 1])); // the entry may be on a page that is not loaded yet
    }
  }
  emit("select", { dn });
}
