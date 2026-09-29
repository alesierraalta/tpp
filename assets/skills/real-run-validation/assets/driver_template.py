"""Real-run validation driver (template).

NOT a unit test. A throwaway script that exercises the REAL built artifact
with synthetic inputs and PRINTS observable behavior, so a human/agent can
confirm it actually works. Keep this in a scratchpad/temp dir, never in the
repo diff. Run it from a fresh env (new venv, HOME=$(mktemp -d)), never against
production, shared environments, or real credentials.

Adapt: swap the import, the input, the expected value, and the calls.
"""
from __future__ import annotations

import importlib.metadata
import sys

# 1) Import the REAL installed module under test, not the source tree.
# import my_pkg
# from my_pkg.some_module import Thing, ThingError

SYNTHETIC_INPUT = b"a plausible synthetic payload, not foo/bar"


def line(title: str) -> None:
    print("\n" + "=" * 64 + f"\n{title}\n" + "=" * 64)


def check(actual: object, expected: object) -> bool:
    """Compare against the contract's expected value; print and return the verdict."""
    ok = actual == expected
    print(f"matches contract?: {ok}")
    return ok


def main() -> int:
    line("ARTIFACT IDENTITY - which build actually ran")
    print(f"python  : {sys.executable}")
    # print(f"dist    : my-dist {importlib.metadata.version('my-dist')}")
    # print(f"import  : {my_pkg.__file__}")  # must be site-packages, not the checkout

    ok = True
    # thing = Thing(...)  # construct with synthetic config

    line("HAPPY PATH - expected effect / exact round-trip")
    # result = thing.do(SYNTHETIC_INPUT)
    # print(f"input   : {SYNTHETIC_INPUT!r}")
    # print(f"output  : {result!r}")
    # ok &= check(result, SYNTHETIC_INPUT)

    line("NEGATIVE CONTROL - the contract must reject this input")
    # try:
    #     thing.do(b"")  # an input the contract forbids
    #     print("!!! BUG: rejected input was accepted !!!")
    #     ok = False
    # except ThingError as exc:
    #     print(f"rejected -> {type(exc).__name__}: {exc}")

    print("\nCompare every observed line above against the intended contract.")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
