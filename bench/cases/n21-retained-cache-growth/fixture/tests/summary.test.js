import { test } from "node:test";
import assert from "node:assert/strict";
import { SummaryCache } from "../src/summary.js";

const records = (n) => Array.from({ length: n }, (_, i) => ({ amount: i + 1 }));

test("summarize counts and totals the records", () => {
  const cache = new SummaryCache();
  assert.deepEqual(cache.summarize("acme", "2026-02-01", records(3)), { count: 3, total: 6 });
});

test("a retained tenant-day answers without recomputing", () => {
  const cache = new SummaryCache();
  const first = cache.summarize("acme", "2026-02-01", records(2));
  const again = cache.summarize("acme", "2026-02-01", records(99));
  assert.equal(again, first);
  assert.equal(cache.cacheEntryCount(), 1);
});

test("get answers with the retained summary and null when unknown", () => {
  const cache = new SummaryCache();
  cache.summarize("acme", "2026-02-01", records(2));
  assert.deepEqual(cache.get("acme", "2026-02-01"), { count: 2, total: 3 });
  assert.equal(cache.get("acme", "2026-02-02"), null);
  assert.equal(cache.get("globex", "2026-02-01"), null);
});

test("latestFor answers with the tenant's latest summary", () => {
  const cache = new SummaryCache();
  assert.equal(cache.latestFor("acme"), null);
  cache.summarize("acme", "2026-02-01", records(1));
  const latest = cache.summarize("acme", "2026-02-02", records(4));
  assert.equal(cache.latestFor("acme"), latest);
  assert.equal(cache.latestFor("globex"), null);
});

test("a full cache releases the oldest tenant-day", () => {
  const cache = new SummaryCache(2);
  cache.summarize("a", "2026-02-01", records(1));
  cache.summarize("b", "2026-02-01", records(2));
  cache.summarize("c", "2026-02-01", records(3));
  assert.equal(cache.get("a", "2026-02-01"), null);
  assert.ok(cache.get("b", "2026-02-01"));
  assert.ok(cache.get("c", "2026-02-01"));
});

test("the default cache retains its full 64 summaries", () => {
  const cache = new SummaryCache();
  for (let i = 1; i <= 64; i++) cache.summarize(`tenant-${i}`, "2026-02-01", records(1));
  assert.equal(cache.cacheEntryCount(), 64);
  assert.ok(cache.get("tenant-1", "2026-02-01"));
  assert.ok(cache.get("tenant-64", "2026-02-01"));
});
