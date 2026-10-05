# dashboard-loader

Loads the three panels of the ops dashboard from independent sources.

- `new DashboardLoader(client)` binds the loader to any client offering
  `profile()`, `orders()` and `activity()`; each answers with a promise for its
  own panel, and none of the three needs another one's answer.
- `load()` resolves with `{ profile, orders, activity }`, the three panels exactly
  as the client returned them.
- The three requests are issued together: `load()` starts all three before waiting
  for any of them, so during a load all three are in flight at once: a caller that
  counts in-flight requests sees a peak of 3, with no timers and no network needed.
