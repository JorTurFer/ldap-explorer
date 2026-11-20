import test from "node:test";
import assert from "node:assert/strict";
import { describeRequest, formatDuration, entriesText, createTracker, outcomeText, clockTime, SHOW_AFTER_MS, SLOW_AFTER_MS, QUIET_AFTER_MS, DONE_SHOWN_MS, HISTORY_LIMIT } from "../js/activity-model.js";

test("describeRequest names what the user asked for", () => {
  const s = describeRequest("POST", "/api/search", { base: "ou=people,dc=x", scope: "sub", filter: "(uid=bob)" });
  assert.equal(s.label, "Searching in ou=people for (uid=bob)");
  assert.equal(s.quiet, false);
  assert.match(describeRequest("POST", "/api/search/ldif", { base: "dc=x", filter: "(a=b)" }).label, /^Exporting in dc=x/);
  assert.equal(describeRequest("GET", "/api/children?dn=ou%3Dpeople%2Cdc%3Dx").label, "Loading the entries under ou=people");
  assert.equal(describeRequest("GET", "/api/entry?dn=uid%3Dalice%2Cdc%3Dx").label, "Reading uid=alice");
  assert.equal(describeRequest("GET", "/api/schema").label, "Loading the server's schema");
});

test("describeRequest keeps long text short", () => {
  const d = describeRequest("POST", "/api/search", { base: "dc=x", filter: "(cn=" + "x".repeat(200) + ")" });
  assert.ok(d.label.length < 120, d.label);
  assert.match(d.label, /…$/);
});

test("the app's own housekeeping is never shown", () => {
  for (const p of ["/api/ping", "/api/activity", "/api/profiles", "/api/filter", "/api/settings", "/api/history", "/api/bookmarks", "/api/queries", "/api/credentials/status"]) {
    assert.equal(describeRequest("GET", p).quiet, true, p);
  }
});

test("formatDuration", () => {
  assert.equal(formatDuration(800), "0.8 s");
  assert.equal(formatDuration(12400), "12 s");
  assert.equal(formatDuration(65000), "1 min 05 s");
  assert.equal(formatDuration(-5), "0.0 s");
  assert.equal(entriesText(1), "1 entry");
  assert.equal(entriesText(1500), "1,500 entries");
});

const search = describeRequest("POST", "/api/search", { base: "dc=x", scope: "sub", filter: "(a=b)" });

test("a fast request never reaches the bar", () => {
  const t = createTracker();
  t.start("a", search, 0);
  assert.equal(t.view(SHOW_AFTER_MS - 1).busy, false);
  t.finish("a", 300);
  assert.equal(t.view(310).done, null); // not worth announcing either
});

test("a running request shows what it is, how long and how many entries", () => {
  const t = createTracker();
  t.start("a", search, 0);
  t.progress("a", { entries: 1500, pages: 3, quietMs: 0 }, 2000);
  const v = t.view(2500);
  assert.equal(v.busy, true);
  assert.equal(v.label, search.label);
  assert.deepEqual(v.parts, ["2.5 s", "1,500 entries so far"]);
  assert.equal(v.level, "ok");
  assert.equal(v.fraction, null); // nothing to compare with yet
});

test("after one run, the next knows what to expect and fills the bar", () => {
  const t = createTracker();
  t.start("a", search, 0);
  t.finish("a", 8000);
  t.start("b", search, 10000);
  const v = t.view(14000);
  assert.ok(v.parts.includes("usually about 8.0 s"), v.parts.join("|"));
  assert.equal(v.fraction, 0.5);
  assert.equal(v.level, "ok");
  assert.equal(t.view(10000 + 8000 * 2).level, "slow"); // 16 s > 1.5 × 8 s
  assert.equal(t.view(10000 + 400000).fraction, 0.95);  // never claims to be finished
});

test("without the same request before, a few of the same kind give the estimate", () => {
  const t = createTracker();
  for (const [i, d] of [3000, 5000, 4000].entries()) {
    const q = describeRequest("POST", "/api/search", { base: "dc=x", filter: `(n=${i})` });
    t.start("r" + i, q, 0); t.finish("r" + i, d);
  }
  t.start("n", describeRequest("POST", "/api/search", { base: "dc=x", filter: "(new=1)" }), 100000);
  assert.ok(t.view(101000).parts.includes("usually about 4.0 s"));
});

test("an estimate under two seconds is not worth showing", () => {
  const t = createTracker();
  t.start("a", search, 0); t.finish("a", 1000);
  t.start("b", search, 5000);
  assert.ok(!t.view(5600).parts.some((p) => p.startsWith("usually")));
});

test("slow, then stuck when the server shows no sign of life", () => {
  const t = createTracker();
  t.start("a", search, 0);
  assert.equal(t.view(SLOW_AFTER_MS - 1).level, "ok");
  const slow = t.view(SLOW_AFTER_MS + 1);
  assert.equal(slow.level, "slow");
  assert.match(slow.note, /still working/);
  const stuck = t.view(QUIET_AFTER_MS + 1);
  assert.equal(stuck.level, "stuck");
  assert.match(stuck.note, /No answer from the server for 30 s/);
});

test("a page arriving is a sign of life: a long search that keeps delivering is never stuck", () => {
  const t = createTracker();
  t.start("a", search, 0);
  for (let s = 5000; s <= 120000; s += 5000) t.progress("a", { entries: s / 10, pages: s / 5000, quietMs: 0 }, s);
  const v = t.view(121000);
  assert.equal(v.level, "slow");
  assert.match(v.note, /longer than usual/);
});

test("the server's own count of quiet time is used too", () => {
  const t = createTracker();
  t.start("a", search, 0);
  t.progress("a", { entries: 100, pages: 1, quietMs: 45000 }, 1000);
  assert.equal(t.view(2000).level, "stuck");
});

test("the oldest request is the one described and the others are counted", () => {
  const t = createTracker();
  t.start("a", search, 0);
  t.start("b", describeRequest("GET", "/api/schema"), 100);
  const v = t.view(1000);
  assert.equal(v.label, search.label);
  assert.equal(v.others, 1);
});

test("done, cancelled and failed are announced for a few seconds", () => {
  const t = createTracker();
  t.start("a", search, 0);
  t.progress("a", { entries: 600, pages: 2, quietMs: 0 }, 1500);
  t.finish("a", 1800);
  assert.equal(t.view(2000).done.text, "Done in 1.8 s · 600 entries");
  assert.equal(t.view(1800 + DONE_SHOWN_MS + 1).done, null);
  t.start("b", search, 20000); t.finish("b", 23000, "cancelled");
  assert.equal(t.view(23100).done.text, "Cancelled after 3.0 s");
  t.start("c", search, 30000); t.finish("c", 31000, "error");
  assert.equal(t.view(31100).done.text, "Failed after 1.0 s");
  assert.equal(t.view(31100).level, "stuck");
});

test("a failed or cancelled request does not teach the estimate", () => {
  const t = createTracker();
  t.start("a", search, 0); t.finish("a", 9000, "error");
  t.start("b", search, 20000);
  assert.ok(!t.view(21000).parts.some((p) => p.startsWith("usually")));
});

test("finishing or reporting on an unknown request is harmless", () => {
  const t = createTracker();
  t.finish("zzz", 1); t.progress("zzz", { entries: 1, pages: 1 }, 1);
  assert.equal(t.view(10).busy, false);
});

test("list: everything running (oldest first) and the finished ones (newest first)", () => {
  const t = createTracker();
  t.start("a", search, 0, 1000);
  t.start("b", describeRequest("GET", "/api/schema"), 500, 1500);
  t.start("young", describeRequest("GET", "/api/server"), 2900, 3900);
  t.progress("a", { entries: 250, pages: 1, quietMs: 0 }, 2000);
  const l = t.list(3000);
  assert.deepEqual(l.running.map((r) => r.id), ["a", "b"]); // "young" has not reached the bar yet
  assert.deepEqual(l.running[0].parts, ["3.0 s", "250 entries so far"]);
  assert.equal(l.history.length, 0);
  t.finish("b", 3200);
  t.finish("a", 4000, "cancelled");
  const after = t.list(5000);
  assert.deepEqual(after.running.map((r) => r.id), ["young"]);
  assert.deepEqual(after.history.map((h) => [h.label, h.outcome, h.ms, h.startedWall, h.text]), [
    [search.label, "cancelled", 4000, 1000, "Cancelled after 4.0 s"],
    ["Loading the server's schema", "ok", 2700, 1500, "Done in 2.7 s"],
  ]);
});

test("the list keeps only the latest finished requests, and can be cleared", () => {
  const t = createTracker();
  for (let i = 0; i < HISTORY_LIMIT + 5; i++) { t.start("r" + i, search, i * 10000, i); t.finish("r" + i, i * 10000 + 1000); }
  const h = t.list(1e6).history;
  assert.equal(h.length, HISTORY_LIMIT);
  assert.equal(h[0].startedWall, HISTORY_LIMIT + 4); // the newest first
  t.clearHistory();
  assert.equal(t.list(1e6).history.length, 0);
  assert.equal(t.view(1e6).done, null);
});

test("fast requests are not in the history, failed ones always are", () => {
  const t = createTracker();
  t.start("fast", search, 0); t.finish("fast", 100);
  t.start("bad", search, 1000); t.finish("bad", 1100, "error");
  assert.deepEqual(t.list(2000).history.map((h) => h.outcome), ["error"]);
});

test("outcomeText and clockTime", () => {
  assert.equal(outcomeText({ outcome: "ok", ms: 1800, entries: 600 }), "Done in 1.8 s · 600 entries");
  assert.equal(outcomeText({ outcome: "error", ms: 1000, entries: 0 }), "Failed after 1.0 s");
  assert.match(clockTime(Date.UTC(2026, 10, 20, 9, 5, 7)), /^\d\d:\d\d:\d\d$/);
  assert.equal(clockTime(0), "");
});
