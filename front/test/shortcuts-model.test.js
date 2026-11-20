import test from "node:test";
import assert from "node:assert/strict";
import { SHORTCUTS, step, isTypingTarget, describeKeys } from "../js/shortcuts-model.js";

test("a single key runs its action", () => {
  assert.deepEqual(step(null, "/", 0), { action: "quickfind", pending: null });
  assert.deepEqual(step(null, "?", 0), { action: "help", pending: null });
  assert.deepEqual(step(null, "t", 0), { action: "treefilter", pending: null });
});

test("a sequence: the first key waits, the second runs", () => {
  const first = step(null, "g", 100);
  assert.equal(first.action, null);
  assert.deepEqual(first.pending, { keys: ["g"], at: 100 });
  assert.deepEqual(step(first.pending, "s", 500), { action: "tab:search", pending: null });
  assert.deepEqual(step(first.pending, "b", 500), { action: "bookmarks", pending: null });
});

test("the second key must come soon enough", () => {
  const first = step(null, "g", 0);
  // too late: "s" alone is nothing
  assert.deepEqual(step(first.pending, "s", 5000), { action: null, pending: null });
  // but a late "/" still works as its own shortcut
  assert.deepEqual(step(first.pending, "/", 5000), { action: "quickfind", pending: null });
});

test("a wrong second key forgets the sequence, and may start a new one", () => {
  const first = step(null, "g", 0);
  assert.deepEqual(step(first.pending, "z", 100), { action: null, pending: null });
  const again = step(first.pending, "g", 100); // g g: the second g starts over
  assert.deepEqual(again.pending, { keys: ["g"], at: 100 });
  assert.deepEqual(step(first.pending, "t", 100), { action: "treefilter", pending: null });
});

test("unknown keys do nothing", () => {
  for (const k of ["a", "z", "1", "Enter", "Escape", "ArrowDown"]) assert.deepEqual(step(null, k, 0), { action: null, pending: null }, k);
});

test("every shortcut can be reached, and none hides another", () => {
  for (const s of SHORTCUTS) {
    let pending = null, last = null;
    s.keys.forEach((k, i) => { last = step(pending, k, i * 10); pending = last.pending; });
    assert.equal(last.action, s.action, describeKeys(s.keys));
  }
  // no shortcut is the prefix of another (it would make the longer one unreachable)
  for (const a of SHORTCUTS) for (const b of SHORTCUTS) {
    if (a !== b && a.keys.length < b.keys.length) assert.notDeepEqual(b.keys.slice(0, a.keys.length), a.keys, `${describeKeys(a.keys)} hides ${describeKeys(b.keys)}`);
  }
  assert.equal(new Set(SHORTCUTS.map((s) => describeKeys(s.keys))).size, SHORTCUTS.length, "duplicate key combination");
});

test("typing in a box never triggers shortcuts", () => {
  assert.equal(isTypingTarget("INPUT"), true);
  assert.equal(isTypingTarget("textarea"), true);
  assert.equal(isTypingTarget("SELECT"), true);
  assert.equal(isTypingTarget("DIV", true), true);
  assert.equal(isTypingTarget("DIV"), false);
  assert.equal(isTypingTarget("BUTTON"), false);
  assert.equal(isTypingTarget(undefined), false);
});

test("how a combination reads", () => {
  assert.equal(describeKeys(["g", "s"]), "g then s");
  assert.equal(describeKeys(["/"]), "/");
});
