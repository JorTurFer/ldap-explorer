import { $, el } from "./dom.js";
import { api, apiText, apiBytes } from "./api.js";
import { emit, on } from "./events.js";
import { showTab } from "./tabs.js";
import { goTo } from "./tree.js";
import { splitDN, normDN } from "./nav.js";
import { getSchema, isDNAttr, isTimeAttr } from "./schema.js";
import { describeTime, formatBytes, summaryBytes, isPhotoAttr, bytesToBase64 } from "./decode.js";
import { copyBtn, copyText, asyncCopyBtn } from "./ui/copy.js";
import { downloadText, downloadBytes } from "./ui/download.js";
import { toast } from "./ui/toast.js";
import { entryToJSON } from "./table.js";
import { isBookmarked, toggleBookmark } from "./bookmarks.js";
import { getPinned, pin, unpin, openCompare, shortName } from "./compare.js";

let currentDN = null;

// Every attribute row has three cells: the name, its values and (shown when you point at
// the row) the copy buttons. The buttons live in their own cell so the name and value
// cells hold only the data.
export function attrTable(attrs, dn = "") {
  const t = el("table", { class: "attrs" });
  for (const a of attrs || []) {
    const values = a.values || [];
    const v = el("td", { class: "v" });
    const dnAttr = isDNAttr(a.name);
    const timeAttr = isTimeAttr(a.name);
    values.forEach((x, i) => {
      if (a.binary) v.append(binaryValue(dn, a.name, i, x));
      else if (dnAttr) v.append(el("div", {}, el("a", { class: "dnlink", textContent: x, title: "Go to this entry", onclick: () => goTo(x) })));
      else {
        v.append(el("div", {}, x));
        const when = timeAttr ? describeTime(x) : "";
        if (when) v.lastChild.append(el("div", { class: "decoded", textContent: when }));
      }
    });
    const valueButton = a.binary && dn
      ? asyncCopyBtn(async () => bytesToBase64((await apiBytes(valueURL(dn, a.name, 0))).bytes), { label: "base64", title: "Copy the value as base64", what: `Copied the value of ${a.name} as base64` })
      : copyBtn(values[0] ?? "", { label: values.length > 1 ? "first value" : "value", title: "Copy the value", what: `Copied the value of ${a.name}` });
    const actions = el("td", { class: "act" },
      copyBtn(a.name, { label: "name", title: `Copy the attribute name (${a.name})`, what: `Copied ${a.name}` }),
      valueButton,
      ...(values.length > 1 && !a.binary ? [copyBtn(values.join("\n"), { label: `all ${values.length}`, title: `Copy all ${values.length} values, one per line`, what: `Copied ${values.length} values of ${a.name}` })] : []));
    t.append(el("tr", { "data-attr": a.name }, el("td", { class: "k", textContent: a.name }), v, actions));
  }
  return t;
}

const valueURL = (dn, attr, i, download = false) =>
  `/api/entry/value?dn=${encodeURIComponent(dn)}&attr=${encodeURIComponent(attr)}&i=${i}${download ? "&download=1" : ""}`;

// A binary value: its size, a picture if it is one, and a way to save it.
function binaryValue(dn, attr, i, summary) {
  const size = summaryBytes(summary);
  const box = el("div", { class: "bin" }, el("span", { textContent: summary }));
  if (!dn) return box;
  box.append(el("button", { type: "button", class: "small-btn download", textContent: size == null ? "download" : `download (${formatBytes(size)})`,
    title: "Save this value as a file", onclick: async (ev) => {
      ev.stopPropagation();
      try { const { bytes, type, filename } = await apiBytes(valueURL(dn, attr, i, true)); downloadBytes(filename, bytes, type); }
      catch (e) { toast(e.message, "err"); }
    } }));
  if (isPhotoAttr(attr)) {
    const img = el("img", { class: "photo", alt: `${attr} of ${dn}` });
    box.append(img);
    apiBytes(valueURL(dn, attr, i)).then(({ bytes, type }) => { img.src = `data:${type};base64,${bytesToBase64(bytes)}`; }).catch(() => img.remove());
  }
  return box;
}

export async function showEntry(dn) {
  showTab("entry");
  currentDN = dn;
  const pane = $("pane-entry");
  pane.replaceChildren(el("div", { class: "muted", textContent: "Loading…" }));
  try {
    await getSchema(); // so DN-valued attributes can be shown as links
    const e = await api("/api/entry?dn=" + encodeURIComponent(dn) + ($("chkOp").checked ? "&operational=1" : ""));
    pane.replaceChildren(
      el("div", { class: "dn" }, breadcrumb(e.dn), copyBtn(e.dn, { title: "Copy the DN", what: "Copied the DN" }),
        el("a", { class: "jump", textContent: "search from here", onclick: () => emit("search-from", { dn: e.dn }) })),
      ...entryActions(e),
      attrTable(e.attrs, e.dn));
  } catch (err) { pane.replaceChildren(el("div", { class: "err", textContent: err.message })); }
}

export function initEntry() {
  on("bookmarks-changed", () => { const s = $("eStar"); if (s && s.paint) s.paint(); }); // keep the star honest
  on("pin-changed", () => { const p = $("ePin"); if (p && p.paint) p.paint(); });
  on("select", ({ dn }) => showEntry(dn));
  on("disconnected", () => {
    currentDN = null;
    $("pane-entry").replaceChildren(el("div", { class: "empty", textContent: "Select an entry in the tree." }));
  });
  $("chkOp").onchange = () => { if (currentDN) showEntry(currentDN); };
}

// The DN with every ancestor as a link, so one click goes up the tree. The text
// is exactly the DN the server returned.
function breadcrumb(dn) {
  const parts = splitDN(dn);
  const span = el("span", { class: "crumbs" });
  parts.forEach((p, i) => {
    if (i > 0) span.append(dn.slice(parts[i - 1].end, p.start));
    if (i === 0) span.append(el("span", { textContent: p.rdn })); // the entry itself
    else span.append(el("a", { class: "crumb", textContent: p.rdn, title: "Go to this entry", onclick: () => goTo(dn.slice(p.start).trim()) }));
  });
  return span;
}

// What you can do with the whole entry: keep it, take it elsewhere (LDIF, JSON), see which
// groups it is in, compare it with another. Returns the buttons row and the groups box.
function entryActions(e) {
  const ldif = async () => apiText("/api/entry/ldif?dn=" + encodeURIComponent(e.dn) + ($("chkOp").checked ? "&operational=1" : ""));
  const guard = (fn) => async () => { try { await fn(); } catch (err) { toast(err.message, "err"); } };
  const star = el("button", { type: "button", id: "eStar", class: "small-btn star", title: "Keep this entry in your bookmarks", onclick: () => toggleBookmark(e.dn) });
  const paintStar = () => { const on = isBookmarked(e.dn); star.textContent = on ? "★ Bookmarked" : "☆ Bookmark"; star.classList.toggle("on", on); };
  paintStar();
  star.paint = paintStar;

  // pin / compare: the buttons follow what is pinned
  const pinBtn = el("button", { type: "button", id: "ePin", class: "small-btn", onclick: () => (getPinned() && sameDn(getPinned(), e.dn) ? unpin() : pin(e.dn)) });
  const cmpBtn = el("button", { type: "button", id: "eCompare", class: "small-btn primary" });
  const paintPin = () => {
    const p = getPinned();
    const here = p && sameDn(p, e.dn);
    pinBtn.textContent = here ? "Unpin" : p ? "Pin this one instead" : "Pin to compare";
    pinBtn.title = here ? "Stop comparing" : "Pin this entry, then open another one and compare them";
    cmpBtn.classList.toggle("hidden", !p || here);
    if (p && !here) { cmpBtn.textContent = `Compare with ${shortName(p)}`; cmpBtn.title = `Compare ${p} with this entry`; }
  };
  cmpBtn.onclick = () => openCompare(e.dn);
  paintPin();
  pinBtn.paint = paintPin;

  const groupsBox = el("div", { id: "eGroupsBox", class: "groups-box hidden" });
  const groupsBtn = el("button", { type: "button", id: "eGroups", class: "small-btn", textContent: "Groups", title: "Which groups is this entry a member of?",
    onclick: guard(async () => {
      groupsBox.classList.remove("hidden");
      groupsBox.replaceChildren(el("span", { class: "muted", textContent: "Looking…" }));
      const g = await api("/api/entry/groups?dn=" + encodeURIComponent(e.dn));
      groupsBox.replaceChildren(
        el("span", { class: "muted", textContent: "Member of: " }),
        ...(!g.available
          ? [el("span", { class: "muted", textContent: `this server cannot say (${g.reason})` })]
          : g.groups.length
            ? g.groups.map((x) => el("a", { class: "dnlink grp", textContent: x.name, title: x.dn, onclick: () => goTo(x.dn) }))
            : [el("span", { textContent: "no group" })]));
    }) });

  const row = el("div", { class: "entry-actions" },
    star, groupsBtn, pinBtn, cmpBtn,
    el("button", { type: "button", id: "eCopyLdif", class: "small-btn", textContent: "Copy as LDIF", title: "The whole entry in LDIF, to paste into a file or another tool",
      onclick: guard(async () => copyText((await ldif()).text, "Copied the entry as LDIF")) }),
    el("button", { type: "button", id: "eDlLdif", class: "small-btn", textContent: "Download LDIF", title: "Save the entry as an .ldif file",
      onclick: guard(async () => { const { text, filename } = await ldif(); downloadText(filename, text); }) }),
    el("button", { type: "button", id: "eCopyJson", class: "small-btn", textContent: "Copy as JSON", title: "The entry as JSON (binary values are summarised)",
      onclick: () => copyText(entryToJSON(e), "Copied the entry as JSON") }));
  return [row, groupsBox];
}

const sameDn = (a, b) => normDN(a) === normDN(b);
