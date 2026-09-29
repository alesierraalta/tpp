"""Retrieval metrics from ranked chunk IDs: Hit Rate, MRR, Recall, graded nDCG. Stdlib only."""
from __future__ import annotations

import math
from dataclasses import dataclass, field


@dataclass
class RetrievalCase:
    query_id: str
    relevance: dict[str, int]  # chunk_id -> grade (>0 relevant; binary labels use 1)
    retrieved: list[str] = field(default_factory=list)


def _validate(cases: list[RetrievalCase], k: int) -> None:
    if not isinstance(k, int) or isinstance(k, bool) or k <= 0:
        raise ValueError("k must be a positive integer")
    if not cases:
        raise ValueError("cases must not be empty")
    for c in cases:
        if not any(g > 0 for g in c.relevance.values()):
            raise ValueError(f"case {c.query_id!r} has no relevant chunk")
        if len(c.retrieved) != len(set(c.retrieved)):
            raise ValueError(f"case {c.query_id!r} has duplicate retrieved IDs")


def _ndcg(c: RetrievalCase, k: int) -> float:
    gain = lambda g: 2 ** g - 1
    dcg = sum(gain(c.relevance.get(cid, 0)) / math.log2(r + 1)
              for r, cid in enumerate(c.retrieved[:k], start=1))
    ideal = sorted(c.relevance.values(), reverse=True)[:k]
    idcg = sum(gain(g) / math.log2(r + 1) for r, g in enumerate(ideal, start=1))
    return dcg / idcg


def metrics(cases: list[RetrievalCase], k: int = 5) -> dict[str, float]:
    _validate(cases, k)
    hit = rr = rec = nd = 0.0
    for c in cases:
        top = c.retrieved[:k]
        rel = {i for i, g in c.relevance.items() if g > 0}
        found = [r for r, cid in enumerate(top, start=1) if cid in rel]
        hit += bool(found)
        rr += 1.0 / found[0] if found else 0.0
        rec += len(rel.intersection(top)) / len(rel)
        nd += _ndcg(c, k)
    n = len(cases)
    return {f"hit_rate@{k}": round(hit / n, 4), f"mrr@{k}": round(rr / n, 4),
            f"recall@{k}": round(rec / n, 4), f"ndcg@{k}": round(nd / n, 4)}


if __name__ == "__main__":
    good = [
        RetrievalCase("q1", {"c10": 2, "c11": 1}, ["c10", "c20", "c11"]),
        RetrievalCase("q2", {"c45": 1}, ["c11", "c45", "c99"]),
        RetrievalCase("q3", {"c88": 1}, ["c01", "c02", "c03"]),
    ]
    print("good:", metrics(good, k=3))
    # negative control: a metric that does not drop on shuffled/unrelated results is not a gate
    broken = [RetrievalCase(c.query_id, c.relevance, ["x1", "x2", "x3"]) for c in good]
    print("unrelated control:", metrics(broken, k=3))
