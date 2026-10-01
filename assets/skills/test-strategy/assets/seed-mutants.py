#!/usr/bin/env python3
"""Seed sealed defects into a throwaway worktree for testing-skill calibration.

Applies K random mutations (deterministic with --seed) to the given source files
and writes the answer key outside the repository. Refuses to run on anything that
is not a throwaway worktree (branch name must start with ``calib-`` or ``skilltest-``).

Usage:
  seed-mutants.py --root <worktree> --files <paths...> --count K --key-out <path> [--seed N]
  seed-mutants.py --root <worktree> --files <paths...> --list
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import random
import re
import subprocess
import sys
from dataclasses import dataclass, asdict

ALLOWED_BRANCH_PREFIXES = ("calib-", "skilltest-")
JS_EXT = {".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
PY_EXT = {".py"}

# Lines matching these are never mutated: too destructive or not meaningful.
FORBIDDEN_LINE = re.compile(r"\.commit\(|\bINSERT\b|\bUPDATE\b|\bDELETE\b|\bDROP\b", re.IGNORECASE)
COMMENT_LINE = re.compile(r"^\s*(//|#|/\*|\*)")


@dataclass
class Site:
    file: str
    line: int  # 1-based
    operator: str
    original: str
    mutated: str



def _inside_string(line: str, idx: int) -> bool:
    """True when position idx sits inside a quoted or template string literal."""
    in_q = None
    i = 0
    while i < idx:
        ch = line[i]
        if ch == "\\":
            i += 2
            continue
        if in_q is None and ch in "\"'`":
            in_q = ch
        elif in_q == ch:
            in_q = None
        i += 1
    return in_q is not None

def _flip_comparison(line: str, lang: str) -> list[tuple[str, str]]:
    out = []
    # Order matters: longer tokens first so ">=" is not caught by ">".
    pairs = [(">=", ">"), ("<=", "<"), ("===", "!=="), ("!==", "==="), ("==", "!="), ("!=", "==")]
    if lang == "py":
        pairs = [(">=", ">"), ("<=", "<"), ("==", "!="), ("!=", "==")]
    for a, b in pairs:
        # Match the operator with non-operator neighbors so ">=" does not match inside "=>".
        pat = re.compile(r"(?<![<>=!])" + re.escape(a) + r"(?![<>=])")
        m = pat.search(line)
        if m and not _inside_string(line, m.start()):
            out.append((f"flip-comparison {a}->{b}", line[: m.start()] + b + line[m.end():]))
            break
    # Also allow ">"->">=" and "<"->"<=" when a bare comparison is present.
    for a, b in [(">", ">="), ("<", "<=")]:
        pat = re.compile(r"(?<![<>=!\-])" + re.escape(a) + r"(?![<>=])")
        m = pat.search(line)
        if m and not _inside_string(line, m.start()) and "=>" not in line and "->" not in line and "<<" not in line and ">>" not in line:
            out.append((f"flip-comparison {a}->{b}", line[: m.start()] + b + line[m.end():]))
            break
    return out


def _swap_logical(line: str, lang: str) -> list[tuple[str, str]]:
    if lang == "py":
        if re.search(r"\band\b", line):
            return [("swap-logical and->or", re.sub(r"\band\b", "or", line, count=1))]
        if re.search(r"\bor\b", line):
            return [("swap-logical or->and", re.sub(r"\bor\b", "and", line, count=1))]
        return []
    if "&&" in line:
        return [("swap-logical &&->||", line.replace("&&", "||", 1))]
    if "||" in line and "??" not in line:
        return [("swap-logical ||->&&", line.replace("||", "&&", 1))]
    return []


def _negate_if(line: str, lang: str) -> list[tuple[str, str]]:
    if lang == "py":
        m = re.match(r"^(\s*(?:el)?if )(.+):\s*$", line)
        if m and not m.group(2).startswith("not ("):
            return [("negate-if", f"{m.group(1)}not ({m.group(2)}):")]
        return []
    m = re.match(r"^(\s*(?:\}\s*else\s+)?if\s*\()(.+)(\)\s*\{?\s*)$", line)
    if m and not m.group(2).startswith("!("):
        return [("negate-if", f"{m.group(1)}!({m.group(2)}){m.group(3)}")]
    return []


def _flip_bool_return(line: str, lang: str) -> list[tuple[str, str]]:
    if lang == "py":
        pairs = [("return True", "return False"), ("return False", "return True")]
    else:
        pairs = [("return true", "return false"), ("return false", "return true")]
    for a, b in pairs:
        if re.search(r"\b" + re.escape(a) + r"\b", line):
            return [(f"flip-bool-return {a.split()[1]}->{b.split()[1]}", re.sub(r"\b" + re.escape(a) + r"\b", b, line, count=1))]
    return []


def _drop_await(line: str, lang: str) -> list[tuple[str, str]]:
    if lang == "py":
        return []  # Python await removal changes semantics too broadly; TS/JS only
    if re.search(r"\bawait\s+", line):
        return [("drop-await", re.sub(r"\bawait\s+", "", line, count=1))]
    return []


OPERATORS = [_flip_comparison, _swap_logical, _negate_if, _flip_bool_return, _drop_await]


def lang_of(path: str) -> str | None:
    ext = os.path.splitext(path)[1]
    if ext in JS_EXT:
        return "js"
    if ext in PY_EXT:
        return "py"
    return None


def candidate_sites(root: str, rel: str) -> list[Site]:
    lang = lang_of(rel)
    if lang is None:
        return []
    abs_path = os.path.join(root, rel)
    with open(abs_path, encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    sites: list[Site] = []
    for idx, line in enumerate(lines):
        if not line.strip() or COMMENT_LINE.match(line) or FORBIDDEN_LINE.search(line):
            continue
        for op in OPERATORS:
            for name, mutated in op(line, lang):
                if mutated != line:
                    sites.append(Site(rel, idx + 1, name, line, mutated))
    return sites


def refuse_unless_throwaway(root: str) -> None:
    try:
        branch = subprocess.check_output(
            ["git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD"], text=True, stderr=subprocess.STDOUT
        ).strip()
    except (subprocess.CalledProcessError, FileNotFoundError) as exc:
        sys.exit(f"REFUSED: {root} is not a git worktree ({exc}).")
    if not branch.startswith(ALLOWED_BRANCH_PREFIXES):
        sys.exit(
            f"REFUSED: branch '{branch}' at {root} is not a throwaway worktree. "
            f"Calibration only runs on a branch named calib-* or skilltest-*."
        )


def syntax_ok(root: str, rel: str) -> bool:
    """Parse-check a mutated file so a seed never breaks module loading (that would be an invalid seed)."""
    abs_path = os.path.join(root, rel)
    if rel.endswith(".py"):
        return subprocess.run([sys.executable, "-m", "py_compile", abs_path], capture_output=True).returncode == 0
    esbuild = os.path.join(root, "node_modules", ".bin", "esbuild")
    if os.path.exists(esbuild):
        return subprocess.run([esbuild, abs_path, "--log-level=error"], capture_output=True).returncode == 0
    return True  # no checker available: accept


def revert_site(root: str, s: "Site") -> None:
    abs_path = os.path.join(root, s.file)
    with open(abs_path, encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    if lines[s.line - 1] == s.mutated:
        lines[s.line - 1] = s.original
        with open(abs_path, "w", encoding="utf-8") as fh:
            fh.write("\n".join(lines))



def apply_sites(root: str, chosen: list[Site]) -> None:
    by_file: dict[str, list[Site]] = {}
    for s in chosen:
        by_file.setdefault(s.file, []).append(s)
    for rel, sites in by_file.items():
        abs_path = os.path.join(root, rel)
        with open(abs_path, encoding="utf-8") as fh:
            lines = fh.read().split("\n")
        for s in sites:
            if lines[s.line - 1] != s.original:
                sys.exit(f"REFUSED: {rel}:{s.line} changed between scan and apply.")
            lines[s.line - 1] = s.mutated
        with open(abs_path, "w", encoding="utf-8") as fh:
            fh.write("\n".join(lines))


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, help="throwaway worktree root")
    ap.add_argument("--files", nargs="+", required=True, help="source files relative to root")
    ap.add_argument("--count", type=int, default=5, help="number of mutations to apply (default 5)")
    ap.add_argument("--key-out", help="answer key path, must be outside --root")
    ap.add_argument("--seed", type=int, default=None, help="RNG seed for a reproducible pick")
    ap.add_argument("--list", action="store_true", help="dry run: print candidate site counts per file")
    ap.add_argument("--replay", help="re-apply the exact mutations of an existing answer key (same seeds across skill versions)")
    args = ap.parse_args()

    root = os.path.realpath(args.root)
    if args.replay:
        refuse_unless_throwaway(root)
        with open(args.replay, encoding="utf-8") as fh:
            chosen = [Site(**entry) for entry in json.load(fh)]
        apply_sites(root, chosen)
        print(f"replayed: {len(chosen)}")
        print("files touched: " + ", ".join(sorted({s.file for s in chosen})))
        print(f"key: {os.path.realpath(args.replay)}")
        return
    refuse_unless_throwaway(root)

    all_sites: list[Site] = []
    per_file: dict[str, int] = {}
    for rel in args.files:
        sites = candidate_sites(root, rel)
        per_file[rel] = len(sites)
        all_sites.extend(sites)

    if args.list:
        for rel, n in per_file.items():
            print(f"{n:5d}  {rel}")
        print(f"total candidate sites: {len(all_sites)}")
        return

    if not args.key_out:
        sys.exit("REFUSED: --key-out is required unless --list.")
    key_out = os.path.realpath(args.key_out)
    if key_out.startswith(root + os.sep):
        sys.exit("REFUSED: --key-out must be outside the worktree.")
    if len(all_sites) < args.count:
        sys.exit(f"REFUSED: only {len(all_sites)} candidate sites for {args.count} mutations.")

    rng = random.Random(args.seed)
    chosen: list[Site] = []
    used_lines: set[tuple[str, int]] = set()
    rejected = 0
    for s in rng.sample(all_sites, k=len(all_sites)):
        key = (s.file, s.line)
        if key in used_lines:
            continue
        used_lines.add(key)
        apply_sites(root, [s])
        if not syntax_ok(root, s.file):
            revert_site(root, s)
            rejected += 1
            continue
        chosen.append(s)
        if len(chosen) == args.count:
            break
    if len(chosen) < args.count:
        for s in chosen:
            revert_site(root, s)
        sys.exit(f"REFUSED: only {len(chosen)} syntactically valid seeds found ({rejected} rejected).")
    if rejected:
        print(f"rejected {rejected} candidate(s) that broke parsing")

    os.makedirs(os.path.dirname(key_out) or ".", exist_ok=True)
    payload = json.dumps([asdict(s) for s in chosen], indent=2, ensure_ascii=False)
    with open(key_out, "w", encoding="utf-8") as fh:
        fh.write(payload)
    digest = hashlib.sha256(payload.encode("utf-8")).hexdigest()

    print(f"applied: {len(chosen)}")
    print("files touched: " + ", ".join(sorted({s.file for s in chosen})))
    print(f"key: {key_out}")
    print(f"key sha256: {digest}")


if __name__ == "__main__":
    main()
