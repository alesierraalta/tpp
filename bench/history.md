# Benchmark history

One row per `tpp bench run`; never rewritten.

| ts | out | model | cases | defects | found | recall | false positives | cost USD | skill version |
|---|---|---|---|---|---|---|---|---|---|
| 2026-09-09T23:12:58Z | bench/results/20260909-231258 | sonnet | 15 | 27 | 10 | 0.37 | 0 | 4.750 | 3.0 |
| 2026-09-10T02:06:23Z | bench/results/20260910-020623 | sonnet | 10 | 17 | 10 | 0.59 | 2 | 5.177 | 3.0 |

| ts | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-09-10T02:32:51Z | bench/results/20260910-023251 | sonnet | 15 | 27 | 15 | 0.56 | 11 | 0.41 | 1 | 0 | 0 | 6 | 8.368 | 3.0 |
| 2026-09-10T09:56:13Z | bench/results/20260910-023251/rescored | sonnet | 15 | 27 | 15 | 0.56 | 20 | 0.74 | 1 | 0 | 0 | 6 | 8.368 | rescore of bench/results/20260910-023251 |
| 2026-09-10T09:56:29Z | bench/results/20260910-095629 | sonnet | 15 | 27 | 24 | 0.89 | 19 | 0.70 | 0 | 0 | 0 | 0 | 8.042 | 3.1 |

| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-09-10T10:26:59Z | run | bench/results/20260910-102659 | sonnet | 15 | 27 | 26 | 0.96 | 21 | 0.78 | 0 | 0 | 0 | 0 | 9.820 | 3.2 |
| 2026-09-10T10:26:59Z | rescore of bench/results/20260910-102659 | bench/results/20260910-102659/rescored | sonnet | 15 | 27 | 26 | 0.96 | 21 | 0.78 | 0 | 0 | 0 | 0 | 0.000 | 3.2 |
| 2026-09-10T10:26:59Z | rescore of bench/results/20260910-102659 | bench/results/20260910-102659/rescored | sonnet | 15 | 27 | 26 | 0.96 | 22 | 0.81 | 0 | 0 | 0 | 0 | 0.000 | 3.2 |
| 2026-09-10T02:32:51Z | rescore of bench/results/20260910-023251 | bench/results/20260910-023251/rescored | sonnet | 15 | 27 | 15 | 0.56 | 20 | 0.74 | 1 | 0 | 0 | 6 | 0.000 | 3.0 |
| 2026-09-10T09:56:29Z | rescore of bench/results/20260910-095629 | bench/results/20260910-095629/rescored | sonnet | 15 | 27 | 24 | 0.89 | 20 | 0.74 | 0 | 0 | 0 | 0 | 0.000 | 3.1 |
| 2026-09-10T10:26:59Z | rescore of bench/results/20260910-102659 | bench/results/20260910-102659/rescored | sonnet | 15 | 27 | 26 | 0.96 | 22 | 0.81 | 0 | 0 | 0 | 0 | 0.000 | 3.2 |
| 2026-09-10T02:32:51Z | rescore of bench/results/20260910-023251 | bench/results/20260910-023251/rescored | sonnet | 15 | 27 | 14 | 0.52 | 20 | 0.74 | 1 | 0 | 0 | 6 | 0.000 | 3.0 |
| 2026-09-10T09:56:29Z | rescore of bench/results/20260910-095629 | bench/results/20260910-095629/rescored | sonnet | 15 | 27 | 22 | 0.81 | 19 | 0.70 | 0 | 0 | 0 | 0 | 0.000 | 3.1 |
| 2026-09-10T10:26:59Z | rescore of bench/results/20260910-102659 | bench/results/20260910-102659/rescored | sonnet | 15 | 27 | 25 | 0.93 | 22 | 0.81 | 0 | 0 | 0 | 0 | 0.000 | 3.2 |
| 2026-09-10T11:08:54Z | run | bench/results/20260910-110854 | sonnet | 15 | 27 | 18 | 0.67 | 23 | 0.85 | 3 | 0 | 0 | 0 | 10.736 | 3.3 |

| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-09-10T11:08:54Z | rescore of bench/results/20260910-110854 | bench/results/20260910-110854/rescored | sonnet | 15 | 27 | 17 | 0.63 | 24 | 0.89 | 3 | 0 | 0 | 0 | 0.000 | 3.3 | bbc93e2+dirty |
| 2026-09-10T11:08:54Z | rescore of bench/results/20260910-110854 | bench/results/20260910-110854/rescored | sonnet | 15 | 27 | 17 | 0.63 | 24 | 0.89 | 3 | 0 | 0 | 0 | 0.000 | 3.3 | bb1df9e+dirty |
| 2026-09-10T02:32:51Z | rescore of bench/results/20260910-023251 | bench/results/20260910-023251/rescored | sonnet | 15 | 27 | 14 | 0.52 | 20 | 0.74 | 1 | 0 | 0 | 6 | 0.000 | 3.0 | bb1df9e+dirty |
| 2026-09-10T09:56:29Z | rescore of bench/results/20260910-095629 | bench/results/20260910-095629/rescored | sonnet | 15 | 27 | 22 | 0.81 | 20 | 0.74 | 0 | 0 | 0 | 0 | 0.000 | 3.1 | bb1df9e+dirty |
| 2026-09-10T10:26:59Z | rescore of bench/results/20260910-102659 | bench/results/20260910-102659/rescored | sonnet | 15 | 27 | 25 | 0.93 | 22 | 0.81 | 0 | 0 | 0 | 0 | 0.000 | 3.2 | bb1df9e+dirty |
| 2026-09-10T11:08:54Z | rescore of bench/results/20260910-110854 | bench/results/20260910-110854/rescored | sonnet | 15 | 27 | 17 | 0.63 | 24 | 0.89 | 3 | 0 | 0 | 0 | 0.000 | 3.3 | bb1df9e+dirty |
| 2026-09-10T12:10:44Z | run | bench/results/20260910-121044 | sonnet | 15 | 0 | 0 | 0.00 | 0 | 0.00 | 0 | 15 | 0 | 0 | 0.000 | 3.4 | 492d23d+dirty |
| 2026-09-10T12:47:10Z | run | bench/results/20260910-124710 | sonnet | 15 | 27 | 25 | 0.93 | 23 | 0.85 | 0 | 0 | 0 | 0 | 13.956 | 3.4 | 5bb7a4b+dirty |
| 2026-09-10T13:58:16Z | run | bench/results/20260910-135816 | sonnet | 15 | 24 | 22 | 0.92 | 21 | 0.88 | 0 | 2 | 0 | 0 | 12.834 | 3.4 | 5962de5+dirty |

| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer | corpus |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-09-10T17:36:37Z | run | bench/results/20260910-173637 | sonnet | 18 | 31 | 29 | 0.94 | 29 | 0.94 | 0 | 0 | 0 | 0 | 17.884 | 0.3.4 | fcb4ce6 | sha256:cca548cb8aad9c02 |
| 2026-09-11T21:01:10Z | run | bench/results/after-full-c6 | openai-codex/gpt-5.6-luna | 18 | 31 | 28 | 0.90 | 23 | 0.74 | 0 | 0 | 0 | 0 | 0.396 | 0.3.7 | 63de308 | sha256:cca548cb8aad9c02 |

Rows above this header predate the activation column and record no scoped run either way: they are non-activation measurements, not zero-activation ones.
| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer | corpus | light | runs |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-09-13T03:14:39Z | run | bench/results/val-after-clean | openai-codex/gpt-5.6-luna | 18 | 91 | 85 | 0.93 | 77 | 0.85 | 0 | 1 | 0 | 0 | 1.268 | 0.3.8 | b8b6710 | sha256:dfab82bd3e6fc21b | 9 | 3 |
| 2026-09-15T14:37:40Z | run | bench/results/baseline-2ad008f | openai-codex/gpt-5.6-luna | 18 | 93 | 83 | 0.89 | 79 | 0.85 | 0 | 0 | 0 | 0 | 1.655 | 0.3.8 | 2ad008f | sha256:dfab82bd3e6fc21b | 4 | 3 |

Rows above this header predate the agent-config column and carry no agent-config mode, so they cannot be compared with rows that do.
Rows above this header predate the micro column and record no micro plan either way: they are non-activation measurements, not zero-activation ones.
| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer | corpus | light | runs | metrics version | unique defects | unique found | unique confirmed | unique caught | defect runs | controls | precision | pending | out of scope | inconclusive | unstable | agent config | environment | micro |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-10-08T12:25:43Z | run | bench/results/baseline-luna6-b2aff3f | openai-codex/gpt-6-luna | 30 | 135 | 41 | 0.30 | 71 | 0.53 | 0 | 0 | 0 | 20 | 0.684 | 0.5.3 | b2aff3f | sha256:44d062a0c0aa8619 | 7 | 3 | 2 | 45 | 26 | 0 | 34 | 135 | 6 | - | 41 | 0 | 30 | g01-inventory-reserve, g02-config-merge, g03-batch-writer, g04-skipped-guard, n01-csv-rfc4180, n02-money-rounding, n03-subscription-window, n04-slug-normalize, n05-keyset-pagination, n06-retry-policy, n07-tenant-scope, n08-sliding-limiter, n10-order-state, n11-html-escape, n12-path-normalize, n13-vacuous-assert, n14-protected-bug, n15-contract-compat, n16-crash-state, n18-dependency-client, n20-batched-query-amplification, n21-retained-cache-growth, n22-hot-path-scaling, n23-independent-io-serialization | bench | linux/amd64 | 3 |
| 2026-10-08T12:25:43Z | rescore of bench/results/baseline-luna6-b2aff3f | bench/results/baseline-luna6-b2aff3f/rescored | openai-codex/gpt-6-luna | 90 | 135 | 41 | 0.30 | 71 | 0.53 | 0 | 0 | 0 | 20 | 0.000 | 0.5.3 | b2aff3f | sha256:44d062a0c0aa8619 | 7 | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | - | 0 | 0 | 0 | - | unspecified | linux/amd64 | 3 |
| 2026-10-08T18:09:32Z | run | bench/results/measure-luna6-9b0157f | openai-codex/gpt-6-luna | 30 | 135 | 49 | 0.36 | 59 | 0.44 | 0 | 0 | 0 | 0 | 0.758 | 0.5.3 | 9b0157f+dirty | sha256:44d062a0c0aa8619 | 13 | 3 | 2 | 45 | 34 | 0 | 32 | 135 | 6 | - | 45 | 0 | 37 | g01-inventory-reserve, g02-config-merge, g03-batch-writer, g04-skipped-guard, n01-csv-rfc4180, n02-money-rounding, n03-subscription-window, n04-slug-normalize, n07-tenant-scope, n08-sliding-limiter, n09-json-ids, n10-order-state, n11-html-escape, n12-path-normalize, n13-vacuous-assert, n15-contract-compat, n16-crash-state, n17-event-consumer, n18-dependency-client, n19-reconnect-listener-leak, n20-batched-query-amplification, n21-retained-cache-growth, n22-hot-path-scaling, n23-independent-io-serialization | bench | linux/amd64 | 6 |
| 2026-10-08T22:14:18Z | run | bench/results/measure-luna6-061207d | openai-codex/gpt-6-luna | 30 | 135 | 49 | 0.36 | 74 | 0.55 | 0 | 0 | 0 | 4 | 0.732 | 0.5.3 | 061207d+dirty | sha256:44d062a0c0aa8619 | 11 | 3 | 2 | 45 | 31 | 0 | 38 | 135 | 6 | - | 47 | 0 | 25 | g01-inventory-reserve, g02-config-merge, g03-batch-writer, g04-skipped-guard, n01-csv-rfc4180, n02-money-rounding, n03-subscription-window, n04-slug-normalize, n05-keyset-pagination, n06-retry-policy, n07-tenant-scope, n08-sliding-limiter, n09-json-ids, n10-order-state, n11-html-escape, n12-path-normalize, n13-vacuous-assert, n14-protected-bug, n15-contract-compat, n16-crash-state, n17-event-consumer, n18-dependency-client, n19-reconnect-listener-leak, n20-batched-query-amplification, n21-retained-cache-growth, n22-hot-path-scaling, n23-independent-io-serialization | bench | linux/amd64 | 5 |
| 2026-10-08T22:14:18Z | rescore of bench/results/measure-luna6-061207d | bench/results/measure-luna6-061207d/rescored | openai-codex/gpt-6-luna | 90 | 135 | 49 | 0.36 | 74 | 0.55 | 1 | 0 | 0 | 4 | 0.000 | 0.5.3 | 061207d+dirty | sha256:44d062a0c0aa8619 | 11 | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | - | 0 | 0 | 0 | - | unspecified | linux/amd64 | 5 |
