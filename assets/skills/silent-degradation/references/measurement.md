# Measuring what the system cannot tell you

## 1. Name the outcome metric

Per surface, the number that would actually prove the thing works:

| Surface | Outcome metric (not "it ran") |
|---|---|
| Retrieval / RAG | recall@k and MRR on the goldset; groundedness and abstention rate on the answers |
| Ranking / search | precision@k, NDCG; rate of queries with zero results |
| Classifier / extractor | precision, recall and F1 per class — never accuracy alone on imbalanced data |
| ETL / pipeline | rows in vs rows out vs dropped-by-reason; null rate per critical column |
| API / service | correctness of the response body on a golden request set, not just status codes |
| Agent / tooling | task completion rate on a fixed scenario set; wrong-tool rate; loop rate |
| Job / worker | items processed vs items with a valid final state; silent-partial rate |

Write the threshold BEFORE measuring. A number with no threshold generates no decision.

### Oracle types

| Oracle | Fits | Example |
|---|---|---|
| Goldset | quality of retrieval, classification, extraction | recall@k on 100 labeled cases |
| Invariant / property | logic with a checkable law | output sorted; idempotent on re-run |
| Reconciliation | data movement | source count and sum equal sink plus rejects |
| Contract / schema | interfaces | response validates against the schema |
| Reference distribution | drift in inputs or scores | PSI or KS against the frozen baseline |

## 2. Build the goldset (small, frozen, honest)

50–200 cases beats 10.000 unlabeled ones. Take them from REAL traffic, including the
cases that went badly — a goldset built from happy paths measures nothing. Label the
expected outcome, not the expected phrasing. Freeze it in the repo with a version and a
date; every change to it is a commit with a reason.

Cover deliberately: the common case, the rare-but-costly case, the case whose answer is
NOT in the system (it must abstain / return empty), near-duplicates, and one case per
known past incident.

## 3. Validate the metric BEFORE the system — the single highest-value step

Break the pipeline on purpose and confirm the metric moves. If it does not, the metric
is blind and every green it ever produced was noise.

| Deliberate break | Expected effect |
|---|---|
| Empty or truncate the index | Recall collapses |
| Shuffle the retrieved results | Ranking metrics collapse, recall@k roughly holds |
| Replace the context with an unrelated one | Groundedness collapses, abstention rises |
| Drop the reranker / a filter stage | The metric moves, or that stage is doing nothing |
| Swap the embedding model at query time only | Recall collapses (this is the classic prod bug) |
| Corrupt 10% of the input rows | Error/drop counters move by ~10% |
| Return a constant answer for everything | Every quality metric collapses to the baseline |

This is mutation testing applied to the evaluation. A green suite over a deliberately
broken system is the finding, and it outranks anything else you would report. Also try to
satisfy the metric while making the outcome worse (Goodhart); if it stays green, the metric
is the finding. Check drift and segment shifts, and record who changed an alert threshold,
when, and why.

## 4. Attribute the loss — ablation and ceiling

**Ablation** — disable one stage at a time, re-measure. A stage whose removal does not
move the metric is either dead code, misconfigured, or invisible to your metric. All
three are defects; find out which.

**Ceiling analysis** — replace one stage at a time with a PERFECT oracle (hand-pick the
correct chunks, hand-write the correct extraction) and re-measure end to end. The jump
tells you how much quality that stage is costing you. Fix where the ceiling is highest;
never optimize the stage with the smallest headroom.

For RAG specifically, run these two questions in this order: **was the right chunk
retrieved?** (retrieval metric on the goldset) and **given the right chunk, was the
answer right?** (feed the gold context directly). Bad answer with a good retrieval and
bad answer with a bad retrieval are different bugs with opposite fixes.

## 5. Judge nondeterministic output honestly

- For a nondeterministic stage: N runs, report the distribution, not a sample. A
  deterministic stage needs one run over the full oracle set. Same input twice giving
  different answers IS a measurement — record the disagreement rate.
- LLM-as-judge only against an explicit RUBRIC (grounded in context? answers the
  question? invents an entity?), and the judge must be validated against human labels on
  a sample before its verdicts count. An unvalidated judge is one more untested
  component asserting that everything is fine.
- Never assert over model phrasing. Make the behavior you care about deterministic
  (abstention as an empty answer or a flag, citations as ids) and assert on that.

## 6. Wire it so the next regression announces itself

A number in a chat message decays in a day. Leave behind: the goldset in the repo, the
metric computed by a command anyone can run, the baseline committed with its date, and a
threshold that fails CI or pages someone, proven by a synthetic breach that makes it fire
(for Prometheus, `promtool test rules` covers the rule logic only, not delivery to a pager). Plus the per-stage counters from
`silent-loss.md`, with the drop-reason label, exported where they are actually watched.

Ratio alerts beat absolute ones: `dropped/in`, `zero-result queries/total`,
`fallback used/total`, `abstained/total`. A ratio moving is the earliest honest signal
that something upstream changed.

## 7. State the unknown

Anything not covered by the goldset or a counter is UNMEASURED. Write it down as such.
"We do not know the precision on <segment>, nothing measures it" is a legitimate and
valuable finding — far better than a confident number nobody can defend.
