import { test } from "node:test";
import assert from "node:assert/strict";
import { sign, verifyWebhook } from "../src/webhook.js";

const secret = "whsec_test";
const now = 1_700_000_000;

function delivery(event, t = now) {
  const rawBody = JSON.stringify(event);
  return { rawBody, header: `t=${t},v1=${sign(secret, t, rawBody)}` };
}

test("a correctly signed delivery is accepted", () => {
  const { rawBody, header } = delivery({ type: "order.paid", id: "o1" });
  const r = verifyWebhook(secret, header, rawBody, { now });
  assert.equal(r.ok, true);
  assert.deepEqual(r.event, { type: "order.paid", id: "o1" });
});

test("a wrong signature is rejected", () => {
  const { rawBody } = delivery({ type: "order.paid", id: "o1" });
  const r = verifyWebhook(secret, `t=${now},v1=${"00".repeat(32)}`, rawBody, { now });
  assert.deepEqual(r, { ok: false, reason: "bad-signature" });
});

test("a body changed after signing is rejected", () => {
  const { rawBody, header } = delivery({ type: "order.paid", id: "o1" });
  const r = verifyWebhook(secret, header, rawBody.replace("o1", "o2"), { now });
  assert.equal(r.ok, false);
});

test("a delivery older than the tolerance is rejected", () => {
  const { rawBody, header } = delivery({ type: "order.paid", id: "o1" }, now - 301);
  assert.deepEqual(verifyWebhook(secret, header, rawBody, { now }), { ok: false, reason: "stale" });
});

test("a malformed header is rejected", () => {
  const r = verifyWebhook(secret, "garbage", "{}", { now });
  assert.deepEqual(r, { ok: false, reason: "malformed-header" });
});
