// Comparing two entries: pin one, open another, compare. A read-only diff, attribute by attribute.
import { $, el } from "./dom.js";
import { api } from "./api.js";
import { emit, on } from "./events.js";
import { diffEntries, summarize, summaryText } from "./diff.js";
import { copyBtn, copyText, preview } from "./ui/copy.js";
import { toast } from "./ui/toast.js";
import { splitDN } from "./nav.js";

let pinned = null; // the DN of the entry pinned as "A"

export const getPinned = () => pinned;
export const shortName = (dn) => (splitDN(dn)[0]?.rdn.split("=").slice(1).join("=") || dn);

export function pin(dn) { pinned = dn; emit("pin-changed", { dn: pinned }); }
export function unpin() { pinned = null; emit("pin-changed", { dn: null }); }

export function initCompare() {
  on("disconnected", unpin);
  $("cmpClose").onclick = () => $("compareDlg").close();
  $("cmpOnly").onchange = renderRows;
}

let rows = [], names = ["A", "B"];

// Fetches both entries and shows the differences.
export async function openCompare(dnB) {
  if (!pinned) return;
  const operational = $("chkOp").checked ? "&operational=1" : "";
  try {
    const [a, b] = await Promise.all([pinned, dnB].map((dn) => api("/api/entry?dn=" + encodeURIComponent(dn) + operational)));
    rows = diffEntries(a, b);
    names = [shortName(a.dn), shortName(b.dn)];
    $("cmpA").replaceChildren(a.dn, copyBtn(a.dn, { title: "Copy the DN", what: "Copied the DN" }));
    $("cmpB").replaceChildren(b.dn, copyBtn(b.dn, { title: "Copy the DN", what: "Copied the DN" }));
    $("cmpSummary").textContent = summaryText(summarize(rows), names[0], names[1]);
    renderRows();
    $("compareDlg").showModal();
  } catch (e) { toast(e.message, "err"); }
}

function renderRows() {
  const only = $("cmpOnly").checked;
  const shown = rows.filter((r) => !only || r.status !== "same");
  const cell = (values, mark) => el("td", { class: "cmp-cell" }, ...(values.length
    ? values.map((v) => el("div", { class: (mark.includes(v) ? "cmp-mark " : "") + "copy-cell", textContent: v, title: "Click to copy",
      onclick: () => copyText(v, `Copied "${preview(v)}"`) }))
    : [el("span", { class: "muted", textContent: "—" })]));
  const label = { same: "same", different: "differs", onlyA: `only in ${names[0]}`, onlyB: `only in ${names[1]}` };
  $("cmpTable").replaceChildren(
    el("thead", {}, el("tr", {}, ...["Attribute", names[0], names[1], ""].map((t) => el("th", { textContent: t })))),
    el("tbody", {}, ...(shown.length
      ? shown.map((r) => el("tr", { class: "cmp-" + r.status, "data-attr": r.name },
        el("td", { class: "k", textContent: r.name }), cell(r.a, r.removed), cell(r.b, r.added),
        el("td", { class: "cmp-status muted small", textContent: label[r.status] })))
      : [el("tr", {}, el("td", { colSpan: 4, class: "muted", textContent: "The two entries are identical." }))])));
}
