// Search results: the classic list of entries, or a table with chosen columns,
// click-to-sort headers and CSV export. The pure logic lives in table.js.

import { el } from "./dom.js";
import { attrTable, showEntry } from "./entry.js";
import { parseAttrList, collectColumns, buildRows, sortRows, toCSV } from "./table.js";

const MAX_TABLE_ROWS = 1000; // the table is drawn for at most this many rows; the CSV has all of them

let view = "list"; // remembered between searches

// Shows the result of a search in `out`.
//   res: { entries, warning }   requestedAttrs: the raw "Attributes" field   ms: elapsed time
export function showResults(out, res, requestedAttrs, ms) {
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
  const btnCsv = el("button", { id: "btnCsv", textContent: "Export CSV", disabled: state.entries.length === 0,
    onclick: () => downloadCSV(state) });

  out.replaceChildren(
    el("div", { class: "muted mt12", textContent: `${res.entries.length} result(s) in ${ms} ms` }),
    ...(res.warning ? [el("div", { class: "warn", textContent: "⚠ " + res.warning })] : []),
    el("div", { class: "toolbar" }, el("span", { class: "seg" }, btnList, btnTable), btnCsv),
    body);
  render();
}

function listView(state) {
  const items = state.entries.map((e) => el("details", { class: "result" },
    el("summary", {}, e.dn, el("a", { class: "jump", textContent: "view", onclick: (ev) => { ev.preventDefault(); showEntry(e.dn); } })),
    el("div", { class: "body" }, attrTable(e.attrs))));
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
        onclick: () => showEntry(r.dn) })),
      ...r.cells.map((c) => el("td", { textContent: c }))))));

  return el("div", {}, chooser,
    ...(rows.length > MAX_TABLE_ROWS ? [el("div", { class: "muted", textContent: `Showing the first ${MAX_TABLE_ROWS} of ${rows.length} rows. The CSV export contains all of them.` })] : []),
    el("div", { class: "table-wrap" }, table));
}

function downloadCSV(state) {
  const idx = visibleIdx(state);
  const text = toCSV(idx.map((i) => state.columns[i]), currentRows(state), { bom: true });
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv;charset=utf-8" }));
  const stamp = new Date().toISOString().replace(/[:T]/g, "-").slice(0, 19);
  const a = el("a", { href: url, download: `ldap-explorer-${stamp}.csv` });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
