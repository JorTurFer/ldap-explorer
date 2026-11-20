import test from "node:test";
import assert from "node:assert/strict";
import { parseAttrList, cellText, collectColumns, buildRows, sortRows, csvCell, toCSV, entryToObject, entryToJSON, entriesToJSON } from "../js/table.js";

const entries = [
  { dn: "uid=alice,dc=x", attrs: [{ name: "uid", values: ["alice"] }, { name: "mail", values: ["alice@x.org"] }, { name: "objectClass", values: ["top", "person"] }] },
  { dn: "uid=bob,dc=x", attrs: [{ name: "uid", values: ["bob"] }, { name: "Mail", values: ["b1@x.org", "b2@x.org"] }, { name: "jpegPhoto", values: ["(binary, 3 bytes) ffd8ff"], binary: true }] },
  { dn: "uid=u10,dc=x", attrs: [{ name: "uid", values: ["u10"] }] },
];

test("parseAttrList splits on commas and spaces", () => {
  assert.deepEqual(parseAttrList("cn, mail  memberOf,sn"), ["cn", "mail", "memberOf", "sn"]);
  assert.deepEqual(parseAttrList(""), []);
  assert.deepEqual(parseAttrList(undefined), []);
});

test("cellText joins multiple values", () => {
  assert.equal(cellText({ values: ["a", "b"] }), "a; b");
  assert.equal(cellText({}), "");
});

test("collectColumns keeps the requested names, order and spelling", () => {
  assert.deepEqual(collectColumns(entries, ["mail", "uid", "MAIL"]), ["mail", "uid"]);
  assert.deepEqual(collectColumns(entries, ["1.1"]), []);
});

test("collectColumns with nothing requested lists what is there, most frequent first", () => {
  // uid x3, mail/Mail x2 (first spelling kept), objectClass x1, jpegPhoto x1
  assert.deepEqual(collectColumns(entries), ["uid", "mail", "jpegPhoto", "objectClass"]);
});

test("collectColumns with * or + puts explicit names first, then the rest", () => {
  assert.deepEqual(collectColumns(entries, ["mail", "*"]), ["mail", "uid", "jpegPhoto", "objectClass"]);
  assert.deepEqual(collectColumns(entries, ["+"]), ["uid", "mail", "jpegPhoto", "objectClass"]);
});

test("buildRows matches attribute names case-insensitively and fills gaps", () => {
  const rows = buildRows(entries, ["uid", "mail"]);
  assert.deepEqual(rows[0], { dn: "uid=alice,dc=x", cells: ["alice", "alice@x.org"] });
  assert.deepEqual(rows[1].cells, ["bob", "b1@x.org; b2@x.org"]); // "Mail" matched "mail"
  assert.deepEqual(rows[2].cells, ["u10", ""]);
});

test("sortRows: natural order, case-insensitive, either direction", () => {
  const rows = ["u10", "u2", "U1", "u3"].map((v) => ({ dn: "d", cells: [v] }));
  assert.deepEqual(sortRows(rows, 0).map((r) => r.cells[0]), ["U1", "u2", "u3", "u10"]);
  assert.deepEqual(sortRows(rows, 0, "desc").map((r) => r.cells[0]), ["u10", "u3", "u2", "U1"]);
});

test("sortRows: empty cells always last, sorting by DN, input untouched", () => {
  const rows = [{ dn: "b", cells: [""] }, { dn: "c", cells: ["x"] }, { dn: "a", cells: [""] }];
  const before = JSON.stringify(rows);
  assert.deepEqual(sortRows(rows, 0).map((r) => r.dn), ["c", "b", "a"]);
  assert.deepEqual(sortRows(rows, 0, "desc").map((r) => r.dn), ["c", "b", "a"]); // empties stay last, stable
  assert.deepEqual(sortRows(rows, -1).map((r) => r.dn), ["a", "b", "c"]);
  assert.equal(JSON.stringify(rows), before);
});

test("csvCell quotes only when needed", () => {
  assert.equal(csvCell("plain"), "plain");
  assert.equal(csvCell("a,b"), '"a,b"');
  assert.equal(csvCell('say "hi"'), '"say ""hi"""');
  assert.equal(csvCell("line1\nline2"), '"line1\nline2"');
  assert.equal(csvCell(" padded"), '" padded"');
  assert.equal(csvCell(null), "");
  assert.equal(csvCell("ñandú €"), "ñandú €");
});

test("csvCell defuses spreadsheet formulas", () => {
  assert.equal(csvCell("=1+1"), "'=1+1");
  assert.equal(csvCell("@SUM(A1)"), "'@SUM(A1)");
  assert.equal(csvCell("+cmd|' /C calc'!A0"), "'+cmd|' /C calc'!A0");
  assert.equal(csvCell("-2+3"), "'-2+3");
  assert.equal(csvCell("\tx"), "'\tx");
  assert.equal(csvCell("\rx"), "\"'\rx\""); // CR is also a CSV special character, so it is quoted too
  // the apostrophe goes inside the quotes when the cell needs quoting
  assert.equal(csvCell('=HYPERLINK("http://x","y")'), '"\'=HYPERLINK(""http://x"",""y"")"');
});

test("csvCell leaves phone numbers and plain numbers alone", () => {
  assert.equal(csvCell("+34 600 000 001"), "+34 600 000 001");
  assert.equal(csvCell("-5"), "-5");
  assert.equal(csvCell("+1 (555) 010-9999"), "+1 (555) 010-9999");
  // several phone numbers in one cell (values are joined with "; ")
  assert.equal(csvCell("+34 600 000 001; +34 600 000 002"), "+34 600 000 001; +34 600 000 002");
  // but one bad value among them is enough to guard the cell
  assert.equal(csvCell("+34 600 000 001; +cmd|calc"), "'+34 600 000 001; +cmd|calc");
});

test("toCSV: dn column first, quoting, CRLF line endings, trailing CRLF", () => {
  const csv = toCSV(["uid", "mail"], buildRows(entries, ["uid", "mail"]));
  assert.equal(
    csv,
    "dn,uid,mail\r\n" +
      '"uid=alice,dc=x",alice,alice@x.org\r\n' +
      '"uid=bob,dc=x",bob,b1@x.org; b2@x.org\r\n' +
      '"uid=u10,dc=x",u10,\r\n',
  );
});

test("toCSV: optional UTF-8 BOM for Excel", () => {
  assert.ok(!toCSV(["uid"], []).startsWith("\uFEFF"));
  assert.equal(toCSV(["uid"], [], { bom: true }), "\uFEFFdn,uid\r\n");
});

test("toCSV quotes DNs and multi-valued cells with commas", () => {
  const csv = toCSV(["mail"], [{ dn: "uid=a,ou=p,dc=x", cells: ["a@x.org; b@x.org"] }]);
  assert.equal(csv, 'dn,mail\r\n"uid=a,ou=p,dc=x",a@x.org; b@x.org\r\n');
});

test("entries as JSON: dn and attributes with all their values", () => {
  assert.deepEqual(entryToObject(entries[1]), {
    dn: "uid=bob,dc=x",
    attributes: { uid: ["bob"], Mail: ["b1@x.org", "b2@x.org"], jpegPhoto: ["(binary, 3 bytes) ffd8ff"] },
  });
  assert.deepEqual(entryToObject({ dn: "cn=empty" }), { dn: "cn=empty", attributes: {} });
  assert.deepEqual(JSON.parse(entryToJSON(entries[0])), entryToObject(entries[0]));
  const all = JSON.parse(entriesToJSON(entries));
  assert.equal(all.length, 3);
  assert.deepEqual(all.map((e) => e.dn), ["uid=alice,dc=x", "uid=bob,dc=x", "uid=u10,dc=x"]);
  assert.match(entriesToJSON(entries), /\n  /); // pretty-printed: it is meant to be read
});

test("JSON export does not alias the input", () => {
  const o = entryToObject(entries[1]);
  o.attributes.Mail.push("x");
  assert.equal(entries[1].attrs[1].values.length, 2);
});
