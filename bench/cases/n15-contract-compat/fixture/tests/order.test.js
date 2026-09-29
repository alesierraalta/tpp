import { test } from "node:test";
import assert from "node:assert/strict";
import { encodeOrder, decodeOrder } from "../src/order.js";

test("an order survives an encode/decode round trip", () => {
  const order = { id: "o1", totalCents: 1250, currency: "EUR" };
  assert.deepEqual(decodeOrder(encodeOrder(order)), order);
});

test("encoded messages carry the schema version", () => {
  const msg = JSON.parse(encodeOrder({ id: "o1", totalCents: 1, currency: "EUR" }));
  assert.equal(msg.v, 2);
});
