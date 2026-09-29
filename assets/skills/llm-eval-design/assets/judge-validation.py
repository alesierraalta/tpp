"""Judge validation helpers (stdlib): TPR/TNR with Wilson CIs, Rogan-Gladen
correction of a judge pass rate, and a bootstrap CI for the corrected rate.

Labels are booleans: True means the item passes. `human` is ground truth.
Usage: python3 judge-validation.py --selftest
"""
from __future__ import annotations

import argparse
import math
import random


def wilson(k: int, n: int, z: float = 1.96) -> tuple[float, float]:
    if n == 0:
        return (0.0, 1.0)
    p = k / n
    d = 1 + z * z / n
    c = p + z * z / (2 * n)
    m = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return ((c - m) / d, (c + m) / d)


def rates(human: list[bool], judge: list[bool]) -> dict:
    if len(human) != len(judge) or not human:
        raise ValueError("human and judge must be non-empty and equal length")
    tp = sum(h and j for h, j in zip(human, judge))
    tn = sum((not h) and (not j) for h, j in zip(human, judge))
    pos, neg = sum(human), len(human) - sum(human)
    return {
        "tpr": tp / pos if pos else float("nan"),
        "tnr": tn / neg if neg else float("nan"),
        "tpr_ci": wilson(tp, pos),
        "tnr_ci": wilson(tn, neg),
        "n_pos": pos,
        "n_neg": neg,
    }


def rogan_gladen(observed: float, tpr: float, tnr: float) -> float:
    """Corrected pass rate. Unstable when tpr + tnr is near 1; raises then."""
    j = tpr + tnr - 1
    # NaN (a class with no human labels) compares False against anything, so test it explicitly.
    if not math.isfinite(j) or j < 0.1:
        raise ValueError(f"judge unusable: tpr + tnr - 1 = {j:.2f} < 0.1")
    return min(1.0, max(0.0, (observed + tnr - 1) / j))


def bootstrap_corrected(
    human: list[bool], judge: list[bool], judged_production: list[bool],
    iters: int = 2000, seed: int = 0,
) -> tuple[float, float]:
    """95% CI of the corrected pass rate, resampling both calibration pairs and
    production judgments. Iterations with an unusable judge are dropped."""
    rng = random.Random(seed)
    pairs = list(zip(human, judge))
    out = []
    for _ in range(iters):
        s = [rng.choice(pairs) for _ in pairs]
        h, j = [a for a, _ in s], [b for _, b in s]
        try:
            r = rates(h, j)
            prod = [rng.choice(judged_production) for _ in judged_production]
            out.append(rogan_gladen(sum(prod) / len(prod), r["tpr"], r["tnr"]))
        except ValueError:
            continue
    if not out:
        raise ValueError("no usable bootstrap iterations")
    out.sort()
    return (out[int(0.025 * len(out))], out[int(0.975 * len(out)) - 1])


def _selftest() -> None:
    lo, hi = wilson(8, 10)
    assert 0.49 < lo < 0.5 and 0.94 < hi < 0.95, (lo, hi)
    human = [True] * 60 + [False] * 40
    judge = [True] * 54 + [False] * 6 + [True] * 4 + [False] * 36
    r = rates(human, judge)
    assert abs(r["tpr"] - 0.9) < 1e-9 and abs(r["tnr"] - 0.9) < 1e-9
    assert abs(rogan_gladen(0.58, 0.9, 0.9) - 0.6) < 1e-9
    try:
        rogan_gladen(0.5, 0.5, 0.5)
    except ValueError:
        pass
    else:
        raise AssertionError("unusable judge must raise")
    try:
        rogan_gladen(0.5, float("nan"), 0.9)
    except ValueError:
        pass
    else:
        raise AssertionError("a NaN rate (no human positives) must raise, not slip past the guard")
    ci = bootstrap_corrected(human, judge, [True] * 58 + [False] * 42, iters=300)
    assert ci[0] <= ci[1]
    # Rare failures: many resamples hold no human positive; the CI must stay finite.
    rare_h = [True] + [False] * 29
    rare_j = [True] + [False] * 29
    ci = bootstrap_corrected(rare_h, rare_j, [False] * 30, iters=300)
    assert all(math.isfinite(x) for x in ci), ci
    print("selftest ok")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--selftest", action="store_true")
    if ap.parse_args().selftest:
        _selftest()
