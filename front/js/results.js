// Search results: the classic list of entries, or a table with chosen columns,
// click-to-sort headers and CSV export. The pure logic lives in table.js.

import { el } from "./dom.js";
import { attrTable, showEntry } from "./entry.js";
import { copyBtn, copyText, preview } from "./ui/copy.js";
import { parseAttrList, collectColumns, buildRows, sortRows, toCSV, entriesToJSON } from "./table.js";
import { apiText } from "./api.js";
import { downloadText } from "./ui/download.js";
import { toast } from "./ui/toast.js";

const MAX_TABLE_ROWS = 1000; // the table is drawn for at most this many rows; the CSV has all of them

let view = "list"; // remembered between searches

// Shows the result of a search in `out`.
//   res: { entries, warning }   requestedAttrs: the raw "Attributes" field   ms: elapsed time
export function showResults(out, res, requestedAttrs, ms, searchBody) {
  const state = {
    entries: res.entries,
    columns: collectColumns(res.entries, parseAttrList(requestedAttrs)),
    hidden: new Set(),
    sortCol: null, // null = server order, -1 = DN, n = column index
    sortDir: "asc",
  };
  state.rows = buildRows(state.entries, state.columns);

  const body = el("div");
  const render = () => {
    syncButtons();
    body.replaceChildren(view === "table" ? tableView(state, render) : listView(state));
  };

  const btnList = el("button", { id: "viewList", textContent: "List", onclick: () => { view = "list"; render(); } });
  const btnTable = el("button", { id: "viewTable", textContent: "Table", onclick: () => { view = "table"; render(); } });
  const syncButtons = () => {
    btnList.classList.toggle("on", view === "list");
    btnTable.classList.toggle("on", view === "table");
  };
  const none = state.entries.length === 0;
  const btnCsv = el("button", { id: "btnCsv", textContent: "Export CSV", disabled: none, title: "The table as a spreadsheet file", onclick: () => downloadCSV(state) });
  const btnJson = el("button", { id: "btnJson", textContent: "Export JSON", disabled: none, title: "The results as JSON (binary values are summarised)",
    onclick: () => downloadText(exportName("json"), entriesToJSON(state.entries), { mime: "application/json" }) });
  const btnLdif = el("button", { id: "btnLdif", textContent: "Export LDIF", disabled: none || !searchBody, title: "The results in LDIF, with the exact values (photos included)",
    onclick: () => exportLDIF(searchBody) });

  out.replaceChildren(
    el("div", { class: "muted mt12", textContent: `${res.entries.length} result(s) in ${ms} ms` }),
    ...(res.warning ? [el("div", { class: "warn", textContent: "⚠ " + res.warning })] : []),
    el("div", { class: "toolbar" }, el("span", { class: "seg" }, btnList, btnTable), btnCsv, btnJson, btnLdif),
    body);
  render();
}

function listView(state) {
  const items = state.entries.map((e) => el("details", { class: "result" },
    el("summary", {}, e.dn, el("a", { class: "jump", textContent: "view", onclick: (ev) => { ev.preventDefault(); showEntry(e.dn); } }),
      copyBtn(e.dn, { title: "Copy the DN", what: "Copied the DN" })),
    el("div", { class: "body" }, attrTable(e.attrs, e.dn))));
  if (items.length === 1) items[0].open = true;
  return el("div", {}, ...items);
}

// Columns currently shown (indexes into state.columns).
const visibleIdx = (state) => state.columns.map((_, i) => i).filter((i) => !state.hidden.has(i));

// Rows in the current order with only the visible cells.
function currentRows(state) {
  const rows = state.sortCol === null ? state.rows : sortRows(state.rows, state.sortCol, state.sortDir);
  const idx = visibleIdx(state);
  return rows.map((r) => ({ dn: r.dn, cells: idx.map((i) => r.cells[i]) }));
}

function tableView(state, rerender) {
  const idx = visibleIdx(state);

  const chooser = el("details", { class: "cols", id: "colChooser" }, el("summary", { textContent: "Columns" }),
    el("div", { class: "cols-list" }, ...state.columns.map((c, i) => el("label", {},
      el("input", { type: "checkbox", checked: !state.hidden.has(i), onchange: (ev) => {
        if (ev.target.checked) state.hidden.delete(i); else state.hidden.add(i);
        if (state.sortCol === i && state.hidden.has(i)) state.sortCol = null;
        rerender();
      } }), " " + c))));

  const arrow = (col) => (state.sortCol === col ? (state.sortDir === "asc" ? " ▲" : " ▼") : "");
  const sortBy = (col) => {
    if (state.sortCol === col) state.sortDir = state.sortDir === "asc" ? "desc" : "asc";
    else { state.sortCol = col; state.sortDir = "asc"; }
    rerender();
  };
  const th = (label, col) => el("th", { textContent: label + arrow(col), title: "Click to sort", onclick: () => sortBy(col) });

  const rows = currentRows(state);
  const shown = rows.slice(0, MAX_TABLE_ROWS);
  const table = el("table", { class: "rtable" },
    el("thead", {}, el("tr", {}, th("DN", -1), ...idx.map((i) => th(state.columns[i], i)))),
    el("tbody", {}, ...shown.map((r) => el("tr", {},
      el("td", { class: "dn-cell" }, el("a", { class: "jump", textContent: r.dn, title: "Open the entry",
        onclick: () => showEntry(r.dn) }), copyBtn(r.dn, { title: "Copy the DN", what: "Copied the DN" })),
      ...r.cells.map((c) => el("td", { class: "copy-cell", textContent: c, title: c ? "Click to copy" : "",
        onclick: () => { if (c) copyText(c, `Copied "${preview(c)}"`); } }))))));

  return el("div", {}, chooser,
    ...(rows.length > MAX_TABLE_ROWS ? [el("div", { class: "muted", textContent: `Showing the first ${MAX_TABLE_ROWS} of ${rows.length} rows. The CSV export contains all of them.` })] : []),
    el("div", { class: "muted small", textContent: "Click a cell to copy it." }),
    el("div", { class: "table-wrap" }, table));
}

const exportName = (ext) => `ldap-explorer-${new Date().toISOString().replace(/[:T]/g, "-").slice(0, 19)}.${ext}`;

function downloadCSV(state) {
  const idx = visibleIdx(state);
  const text = toCSV(idx.map((i) => state.columns[i]), currentRows(state), { bom: true });
  downloadText(exportName("csv"), text, { mime: "text/csv;charset=utf-8", bom: false }); // toCSV already added the BOM
}

// LDIF is produced by the server (it needs the raw values), from the same search.
async function exportLDIF(searchBody) {
  try {
    const { text, filename, warning } = await apiText("/api/search/ldif", { body: searchBody });
    downloadText(filename, text);
    toast(warning ? `Exported what the server returned (${warning})` : "LDIF exported", warning ? "err" : "");
  } catch (e) { toast(e.message, "err"); }
}
