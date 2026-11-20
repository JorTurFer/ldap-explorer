import test from "node:test";
import assert from "node:assert/strict";
import { createTrail, visit, back, forward, canBack, canForward, current, peekBack, peekForward, samePlace, placeLabel } from "../js/trail.js";

const e = (dn) => ({ tab: "entry", dn });
const tab = (name) => ({ tab: name });
const A = e("uid=a,dc=x"), B = e("uid=b,dc=x"), C = e("uid=c,dc=x");

test("an empty trail goes nowhere", () => {
  const t = createTrail();
  assert.equal(current(t), null);
  assert.equal(canBack(t), false);
  assert.equal(canForward(t), false);
  assert.equal(back(t), t);
});

test("visit, back and forward", () => {
  let t = visit(visit(visit(createTrail(), A), B), C);
  assert.equal(current(t), C);
  assert.equal(peekBack(t), B);
  t = back(back(t));
  assert.equal(current(t), A);
  assert.equal(canBack(t), false);
  assert.equal(peekForward(t), B);
  t = forward(t);
  assert.equal(current(t), B);
});

test("entries and tabs are places of the same trail", () => {
  let t = createTrail();
  for (const p of [A, tab("search"), B, tab("server")]) t = visit(t, p);
  assert.deepEqual(t.items.map(placeLabel), ["uid=a,dc=x", "the Search tab", "uid=b,dc=x", "the Server tab"]);
  t = back(back(t));
  assert.deepEqual(current(t), tab("search"));
  assert.equal(placeLabel(peekBack(t)), "uid=a,dc=x");
});

test("being on the same place again (any letter case or spacing of a DN) changes nothing", () => {
  const t = visit(createTrail(), A);
  assert.equal(visit(t, e("UID=a, dc=x")), t);
  const s = visit(t, tab("search"));
  assert.equal(visit(s, tab("search")), s);
  assert.equal(samePlace(tab("entry"), e("uid=a,dc=x")), false); // the Entry tab alone is not an entry
});

test("visiting something new forgets what was forward", () => {
  let t = visit(visit(createTrail(), A), tab("search"));
  t = back(t);
  t = visit(t, C);
  assert.deepEqual(t.items, [A, C]);
  assert.equal(canForward(t), false);
});

test("the trail is capped", () => {
  let t = createTrail();
  for (let i = 0; i < 250; i++) t = visit(t, e(`uid=u${i},dc=x`));
  assert.equal(t.items.length, 100);
  assert.equal(current(t).dn, "uid=u249,dc=x");
});
