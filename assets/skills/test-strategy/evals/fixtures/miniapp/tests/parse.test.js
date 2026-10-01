import { test } from "node:test";
import assert from "node:assert/strict";
import { parseCsvLine } from "../src/parse.js";

test("splits plain fields on commas", () => {
  assert.deepEqual(parseCsvLine("a,b,c"), ["a", "b", "c"]);
});

test("strips surrounding quotes", () => {
  assert.deepEqual(parseCsvLine('"a",b'), ["a", "b"]);
});

test("empty line yields no fields", () => {
  assert.deepEqual(parseCsvLine(""), []);
});
