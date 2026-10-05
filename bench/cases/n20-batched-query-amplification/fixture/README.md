# order-report

Builds the orders shown on the admin report page from an in-memory repository.

- The repository offers `orders(filter)` (the rows matching the filter), `itemsFor(id)`
  (one order's items) and `itemsForOrders(ids)` (the items of the given orders).
- `new ReportService(repo)` binds the report to that repository.
- `listOrders(filter)` returns each order with its `items` array filled in, exactly as
  the repository stores them.
- A listing reads the repository a fixed number of times — one call for the rows plus
  one batched call for the items — whether the filter matches one order or fifty.
- `statusCounts()` tallies the orders by status; while the underlying rows are unchanged,
  repeated calls answer from the report's own memo and read the repository only once.
