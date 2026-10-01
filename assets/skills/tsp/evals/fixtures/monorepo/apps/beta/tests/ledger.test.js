import { test } from "node:test";
import assert from "node:assert/strict";
import { createLedger } from "../src/ledger.js";

test("posting entries accumulates the balance", () => {
  const ledger = createLedger();
  ledger.post({ id: "a", amount: 10 });
  ledger.post({ id: "b", amount: 2.5 });
  assert.equal(ledger.balance(), 12.5);
});

test("the same id posted twice is ignored", () => {
  const ledger = createLedger();
  assert.deepEqual(ledger.post({ id: "a", amount: 10 }), { posted: true, id: "a" });
  assert.deepEqual(ledger.post({ id: "a", amount: 99 }), { posted: false, id: "a" });
  assert.equal(ledger.balance(), 10);
});

test("entries returns what was posted", () => {
  const ledger = createLedger();
  ledger.post({ id: "x", amount: 1 });
  assert.deepEqual(ledger.entries(), [{ id: "x", amount: 1 }]);
});
