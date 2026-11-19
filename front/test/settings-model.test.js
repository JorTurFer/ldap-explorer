import test from "node:test";
import assert from "node:assert/strict";
import { THEMES, nextTheme, themeLabel, themeAttribute } from "../js/settings-model.js";
import { preview } from "../js/ui/copy.js";

test("the theme cycles auto -> light -> dark -> auto", () => {
  assert.deepEqual(THEMES, ["auto", "light", "dark"]);
  assert.equal(nextTheme("auto"), "light");
  assert.equal(nextTheme("light"), "dark");
  assert.equal(nextTheme("dark"), "auto");
  assert.equal(nextTheme("nonsense"), "auto");
  assert.equal(nextTheme(undefined), "auto");
});

test("theme label and <html> attribute", () => {
  assert.equal(themeLabel("dark"), "Theme: dark");
  assert.equal(themeLabel("weird"), "Theme: auto");
  assert.equal(themeAttribute("light"), "light");
  assert.equal(themeAttribute("dark"), "dark");
  assert.equal(themeAttribute("auto"), null); // no attribute: the system setting decides
  assert.equal(themeAttribute("weird"), null);
});

test("preview shortens long text for a toast", () => {
  assert.equal(preview("short"), "short");
  assert.equal(preview("x".repeat(40)), "x".repeat(40));
  assert.equal(preview("x".repeat(41)), "x".repeat(39) + "…");
  assert.equal(preview("abcdefghij", 5), "abcd…");
});
