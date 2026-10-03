import { test } from "node:test";
import assert from "node:assert/strict";
import { withinLimit, clamp } from "../src/limits.js";

test("a value below the limit is within it", () => {
  assert.equal(withinLimit(5, 10), true);
});

test("a value above the limit is out", () => {
  assert.equal(withinLimit(11, 10), false);
});

test("clamp keeps a value inside the range", () => {
  assert.equal(clamp(15, 0, 10), 10);
  assert.equal(clamp(-3, 0, 10), 0);
  assert.equal(clamp(4, 0, 10), 4);
});
