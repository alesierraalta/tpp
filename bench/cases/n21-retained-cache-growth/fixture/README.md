# tenant-summary

Caches the per-tenant daily summaries the dashboard shows.

- `new SummaryCache(limit)` opens a cache that retains at most `limit` summaries
  (64 by default). `summarize(tenant, day, records)` answers with that tenant-day's
  summary — `{ count, total }` over the records' `amount` — and a repeat for a
  retained tenant-day answers with the stored summary instead of recomputing.
- The cache retains the newest `limit` summaries: one arriving past the limit
  releases the oldest, so after any number of tenant-days the cache never retains
  more than `limit` summaries at a time.
- `get(tenant, day)` answers with that tenant-day's summary while it is retained
  and with `null` once it has been released.
- `latestFor(tenant)` answers with the tenant's most recent retained summary, or
  `null` when the tenant has none retained.
- `cacheEntryCount()` answers with the number of distinct summaries the cache is
  retaining at that moment.
