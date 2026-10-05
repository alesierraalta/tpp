function summarizeRecords(records) {
  let count = 0;
  let total = 0;
  for (const record of records) {
    count += 1;
    total += record.amount;
  }
  return { count, total };
}

export class SummaryCache {
  constructor(limit = 64) {
    this.limit = limit;
    this.entries = new Map();
    this.byTenant = new Map();
  }

  // One tenant-day summary; a retained tenant-day answers without recomputing.
  summarize(tenant, day, records) {
    const key = `${tenant}:${day}`;
    const retained = this.entries.get(key);
    if (retained) return retained;
    const summary = summarizeRecords(records);
    this.entries.set(key, summary);
    this.byTenant.set(tenant, summary);
    this.evict();
    return summary;
  }

  // Keep the cache within its limit.
  evict() {
    while (this.entries.size > this.limit) {
      const oldestKey = this.entries.keys().next().value;
      this.entries.delete(oldestKey);
    }
  }

  // That tenant-day's summary while it is retained, else null.
  get(tenant, day) {
    const summary = this.entries.get(`${tenant}:${day}`);
    return summary === undefined ? null : summary;
  }

  // The tenant's most recent retained summary, else null.
  latestFor(tenant) {
    const summary = this.byTenant.get(tenant);
    return summary === undefined ? null : summary;
  }

  // How many distinct summaries the cache retains right now.
  cacheEntryCount() {
    const retained = new Set(this.entries.values());
    for (const summary of this.byTenant.values()) retained.add(summary);
    return retained.size;
  }
}
