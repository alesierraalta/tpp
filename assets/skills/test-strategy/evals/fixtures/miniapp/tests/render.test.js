import { test } from "node:test";
import assert from "node:assert/strict";
import { renderCsvLine } from "../src/render.js";

test("joins plain fields with commas", () => {
  assert.equal(renderCsvLine(["a", "b", "c"]), "a,b,c");
});

test("quotes a field that contains the separator", () => {
  assert.equal(renderCsvLine(["a", "b,c"]), 'a,"b,c"');
});

test("rejects a non-array input", () => {
  assert.throws(() => renderCsvLine("a,b"), TypeError);
});
