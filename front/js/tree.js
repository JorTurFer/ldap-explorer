import { $, el } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { emit, on } from "./events.js";
import { ancestors, normDN, looksLikeDN, matchesFilter } from "./nav.js";
import { copyBtn } from "./ui/copy.js";
import { childrenURL, loadedText } from "./tree-model.js";
import { friendlyError } from "./friendly-errors.js";

let roots = [];
let selectedNode = null;
const nodes = new Map(); // normalised DN -> { open(), select() } for every node drawn so far

export function initTree() {
  on("connected", ({ roots: rs }) => {
    roots = rs;
    $("btnRefresh").disabled = false;
    $("treeFilter").disabled = false;
    buildTree();
  });
  on("disconnected", () => {
    roots = []; selectedNode = null; nodes.clear();
    $("btnRefresh").disabled = true;
    $("treeFilter").disabled = true; $("treeFilter").value = "";
    $("treeHint").classList.add("hidden");
    $("tree").classList.remove("filtering");
    $("tree").replaceChildren(el("div", { class: "empty", textContent: "Not connected." }));
  });
  $("btnRefresh").onclick = buildTree;

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

  // The "N loaded, more below" row at the end of a long branch. It loads the next page by
  // itself when it scrolls into view (and not while the filter is on: that would read the
  // whole branch just to look for a match), and has buttons for doing it by hand.
  const showMore = (failure = "") => {
    observer?.disconnect();
    if (!cursor && !failure) { moreRow?.remove(); moreRow = null; return; }
    const row = el("li", { class: "more more-row" });
    if (failure) {
      row.append(el("span", { class: "err", textContent: failure }),
        el("button", { type: "button", class: "small-btn", textContent: "Reload this branch", onclick: reload }));
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
        showMore(e.cancelled ? "Cancelled. The rest of this branch is not loaded." : friendlyError(e.message));
        return false;
      } finally { loading = null; }
    })();
    return loading;
  };
  const loadAll = async () => { while (cursor) if (!(await loadMore())) break; };
  const reload = () => { kids?.remove(); kids = null; moreRow = null; cursor = ""; loaded = 0; open = false; toggle(); };

  const toggle = async () => {
    if (n.leaf) return;
    open = !open;
    if (open && !kids) {
      tw.textContent = "…";
      try {
        const res = await api(childrenURL(n.dn));
        kids = el("ul", { class: "t" });
        if (!res.children.length) { n.leaf = true; tw.textContent = ""; return; }
        li.append(kids);
        addPage(res);
      } catch (e) { setStatus(friendlyError(e.message), true); open = false; tw.textContent = "▶"; return; }
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
