# RAG metrics

`assets/retrieval-benchmark.py` requires a non-empty case list, a positive integer `k`, a non-empty relevance map per query, and unique retrieved IDs (duplicates raise `ValueError`; they signal a retriever defect). An empty retrieved list is a valid miss.

## Retrieval

- Hit Rate@K = share of queries with at least one relevant chunk in the top K.
- MRR@K = mean of 1/rank of the first relevant chunk (0 if none).
- Recall@K = relevant chunks in top K / relevant chunks. Use recall@50 to judge the candidate stage and precision@5 to judge the reranker.
- nDCG@K with graded labels (for example 0 irrelevant, 1 partial, 2 fully answers): DCG = sum of (2^rel - 1) / log2(rank + 1), divided by the DCG of the ideal ordering.
- RRF for hybrid: score(d) = sum over rankers of 1 / (k + rank(d)), k = 60 is the common default. Fusion is not proof of improvement: compare sparse-only, dense-only and hybrid per query slice with latency p95 and cost.

## Generation

- Faithfulness: split the answer into atomic claims and label each entailed, contradicted or unsupported against the retrieved context; report the three counts, not only the ratio. Score alone is meaningless without the no-context control and a completeness partner.
- Citation support: per (claim, cited chunk) pair, does the chunk entail the claim; quote fidelity is an exact substring check.
- Abstention: refusal rate on the unanswerable set and refusal rate on the answerable control set.

## Ragas

Ragas metrics (Context Precision, Context Recall, Noise Sensitivity, Response Relevancy, Faithfulness) are listed at https://docs.ragas.io/en/stable/concepts/metrics/available_metrics/. Metric names and semantics changed across releases (verify the installed version before comparing numbers). Response Relevancy (reverse-question similarity) can reward a generic answer, so treat it as a weak signal, never a gate. Sentence-ratio context relevance is not a gate either.

## Sources

- OWASP LLM08 (vector and embedding weaknesses, cross-context leakage): https://genai.owasp.org/llmrisk/llm082025-vector-and-embedding-weaknesses/ (verify the current URL).
