# rule-match

Matches incoming records against the routing rules the ops team maintains.

- A rule carries a `region` and a `channel` — its routing key — and the `route` it
  forwards to; a record carries the `region` and `channel` it arrived with.
- `new RuleMatcher(rules, counter)` takes the rule list and an operation counter
  (`{ count: 0 }` is the shape): every rule the matcher examines adds one to
  `counter.count`. The counter is the whole measurement; nothing here is timed.
- `match(record)` answers with the first rule whose `region` and `channel` both
  equal the record's, or `null` when no rule matches. `matchAll(records)` answers
  with one result per record, in record order.
- Matching is a hot path, and its cost is bounded by the sizes of the input rather
  than by their product: resolving a 100-record batch against a 100-rule set
  performs at most 200 operations — one per rule plus one per record.
