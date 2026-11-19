import { $, el } from "./dom.js";
import { api } from "./api.js";
import { setStatus } from "./status.js";
import { emit, on } from "./events.js";

let roots = [];
let selectedNode = null;

export function initTree() {
  on("connected", ({ roots: rs }) => {
    roots = rs;
    $("btnRefresh").disabled = false;
    buildTree();
  });
  on("disconnected", () => {
    roots = []; selectedNode = null;
    $("btnRefresh").disabled = true;
    $("tree").replaceChildren(el("div", { class: "empty", textContent: "Not connected." }));
  });
  $("btnRefresh").onclick = buildTree;
}

function buildTree() {
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
  const row = el("div", { class: "node", title: n.dn }, tw, el("span", { class: "rdn", textContent: n.rdn }));
  let kids = null, open = false;
  const toggle = async () => {
    if (n.leaf) return;
    open = !open;
    if (open && !kids) {
      tw.textContent = "…";
      try {
        const res = await api("/api/children?dn=" + encodeURIComponent(n.dn));
        kids = el("ul", { class: "t" });
        if (!res.children.length) { n.leaf = true; tw.textContent = ""; return; }
        for (const c of res.children) kids.append(makeNode(c));
        if (res.warning) kids.append(el("li", { class: "more", textContent: "⚠ " + res.warning }));
        li.append(kids);
      } catch (e) { setStatus(e.message, true); open = false; tw.textContent = "▶"; return; }
    }
    if (kids) kids.style.display = open ? "" : "none";
    tw.textContent = open ? "▼" : "▶";
  };
  tw.addEventListener("click", (ev) => { ev.stopPropagation(); toggle(); });
  row.addEventListener("click", () => {
    if (selectedNode) selectedNode.classList.remove("sel");
    row.classList.add("sel"); selectedNode = row;
    emit("select", { dn: n.dn });
  });
  row.addEventListener("dblclick", toggle);
  li.append(row);
  return li;
}
