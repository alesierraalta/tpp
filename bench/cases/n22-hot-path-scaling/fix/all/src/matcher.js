export class RuleMatcher {
  constructor(rules, counter = { count: 0 }) {
    this.rules = rules;
    this.counter = counter;
    this.lookup = null;
  }

  // The rules folded into a lookup once; every later match resolves through it.
  index() {
    if (this.lookup === null) {
      this.lookup = new Map();
      for (const rule of this.rules) {
        this.counter.count += 1;
        const key = JSON.stringify([rule.region, rule.channel]);
        if (!this.lookup.has(key)) this.lookup.set(key, rule);
      }
    }
    return this.lookup;
  }

  // The first rule whose region and channel both match the record.
  match(record) {
    const key = JSON.stringify([record.region, record.channel]);
    return this.index().get(key) ?? null;
  }

  matchAll(records) {
    return records.map((record) => this.match(record));
  }
}
