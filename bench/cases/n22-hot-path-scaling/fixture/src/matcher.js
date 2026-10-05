export class RuleMatcher {
  constructor(rules, counter = { count: 0 }) {
    this.rules = rules;
    this.counter = counter;
  }

  // The first rule whose region and channel both match the record.
  match(record) {
    const hit = this.rules.find((rule) => {
      this.counter.count += 1;
      return rule.region === record.region && rule.channel === record.channel;
    });
    return hit ?? null;
  }

  matchAll(records) {
    return records.map((record) => this.match(record));
  }
}
