import test from "node:test";
import assert from "node:assert/strict";
import { createTrail, visit, back, forward, canBack, canForward, current, peekBack, peekForward } from "../js/trail.js";

const A = "uid=a,dc=x", B = "uid=b,dc=x", C = "uid=c,dc=x";

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

test("visiting the same entry again (in any letter case or spacing) changes nothing", () => {
  const t = visit(createTrail(), A);
  assert.equal(visit(t, "UID=a, dc=x"), t);
});

test("visiting something new forgets what was forward", () => {
  let t = visit(visit(createTrail(), A), B);
  t = back(t);
  t = visit(t, C);
  assert.deepEqual(t.items, [A, C]);
  assert.equal(canForward(t), false);
});

test("the trail is capped", () => {
  let t = createTrail();
  for (let i = 0; i < 250; i++) t = visit(t, `uid=u${i},dc=x`);
  assert.equal(t.items.length, 100);
  assert.equal(current(t), "uid=u249,dc=x");
});
