# Quality audit — <system / pipeline>

## Outcome

- What it must produce: <...>
- Metric that proves it: <...>  Threshold: <...>
- Oracle: <goldset | invariant | reconciliation | contract> <path> (<n> cases, version/date)

## Stage accounting

Declared equation per stage: <e.g. out + dropped == in, or fan-out/aggregation form>

| Stage | in | out | dropped | reason per drop | equation holds? | justified? |
|---|---|---|---|---|---|---|

## Metric validation (deliberate breaks)

| Break applied (incl. Goodhart: satisfy metric, worsen outcome) | Metric before | Metric after | Metric noticed? |
|---|---|---|---|

Alert fire test: <synthetic breach> -> <observed alert / metric change>

## Baseline

| Metric | Value | Goldset version | Date | Threshold |
|---|---|---|---|---|

## Loss attribution

| Stage | Ablated (metric delta) | Perfect-oracle ceiling (metric delta) |
|---|---|---|

## Filters / permissions diff

| Restriction | Items excluded | Justified | Unjustified (defects) |
|---|---|---|---|

## Findings

| # | Type (quality loss / system blindness) | path:line | Evidence | Fix + the counter left behind |
|---|---|---|---|---|

## Unmeasured

<what nothing observes, and what it would take to observe it>
