import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizePath } from "../src/normalize.js";

test("collapses repeated separators", () => {
  assert.equal(normalizePath("a//b///c"), "a/b/c");
});

test("removes a dot segment", () => {
  assert.equal(normalizePath("a/./b"), "a/b");
});

test("rejects a non-string path", () => {
  assert.throws(() => normalizePath(null), TypeError);
});
