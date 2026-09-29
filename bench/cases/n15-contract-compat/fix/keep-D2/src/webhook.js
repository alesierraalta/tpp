import { createHmac, timingSafeEqual } from "node:crypto";

export function sign(secret, timestamp, rawBody) {
  return createHmac("sha256", secret).update(`${timestamp}.${rawBody}`).digest("hex");
}

function parseHeader(header) {
  const parts = Object.fromEntries(
    String(header ?? "").split(",").map((p) => p.trim().split("=")),
  );
  const t = Number(parts.t);
  if (!Number.isInteger(t) || typeof parts.v1 !== "string") return null;
  return { t, v1: parts.v1 };
}

export function verifyWebhook(secret, header, rawBody, { now, toleranceSec = 300 } = {}) {
  const parsed = parseHeader(header);
  if (!parsed) return { ok: false, reason: "malformed-header" };
  if (Math.abs(now - parsed.t) > toleranceSec) return { ok: false, reason: "stale" };

  let event;
  try {
    event = JSON.parse(rawBody);
  } catch {
    return { ok: false, reason: "malformed-body" };
  }

  const expected = Buffer.from(sign(secret, parsed.t, rawBody), "hex");
  const given = Buffer.from(parsed.v1, "hex");
  if (given.length !== expected.length || !timingSafeEqual(given, expected)) {
    return { ok: false, reason: "bad-signature" };
  }
  return { ok: true, event };
}
