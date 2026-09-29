"""pass@k and pass^k estimators from n trials with c successes (stdlib).

pass@k: probability at least one of k sampled trials succeeds.
pass^k: probability all k sampled trials succeed (reliability).
Verify the estimator against the benchmark's own definition before comparing
numbers across papers. Usage: python3 pass-k.py --selftest
"""
from __future__ import annotations

import argparse
from math import comb


def _check(n: int, c: int, k: int) -> None:
    if not (0 <= c <= n) or not (1 <= k <= n):
        raise ValueError(f"need 0 <= c <= n and 1 <= k <= n; got n={n} c={c} k={k}")


def pass_at_k(n: int, c: int, k: int) -> float:
    _check(n, c, k)
    if n - c < k:
        return 1.0
    return 1.0 - comb(n - c, k) / comb(n, k)


def pass_hat_k(n: int, c: int, k: int) -> float:
    _check(n, c, k)
    return comb(c, k) / comb(n, k)


def suite(results: dict[str, tuple[int, int]], k: int) -> dict[str, float]:
    """results maps task -> (n, c); returns the mean of each metric over tasks."""
    if not results:
        raise ValueError("no tasks")
    m = len(results)
    return {
        "pass@k": sum(pass_at_k(n, c, k) for n, c in results.values()) / m,
        "pass^k": sum(pass_hat_k(n, c, k) for n, c in results.values()) / m,
    }


def _selftest() -> None:
    assert pass_at_k(8, 0, 1) == 0.0 and pass_hat_k(8, 8, 8) == 1.0
    assert abs(pass_at_k(4, 2, 1) - 0.5) < 1e-9
    assert abs(pass_hat_k(4, 2, 2) - 1 / 6) < 1e-9
    assert pass_at_k(4, 3, 2) == 1.0
    s = suite({"a": (8, 8), "b": (8, 4)}, k=2)
    assert s["pass^k"] < s["pass@k"]
    try:
        pass_hat_k(4, 5, 1)
    except ValueError:
        pass
    else:
        raise AssertionError("invalid counts must raise")
    print("selftest ok")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--selftest", action="store_true")
    if ap.parse_args().selftest:
        _selftest()
