#!/usr/bin/env python3
"""
run_evals.py — behavioral eval runner for the organic testing skill (test-strategy).

Each case scaffolds a fixture workspace, runs `claude -p "<prompt>"` inside it, and scores the
result with deterministic graders over the final output, the tool calls, and the files the
agent produced. Stdlib only. Workspaces live under evals/.runs/, never /tmp.

Arms:
  with     — normal user config: skills and CLAUDE.md load (the skill under test).
  without  — fresh CLAUDE_CONFIG_DIR, so no skills or CLAUDE.md load (baseline).
  both     — `with` for every selected case, plus `without` for cases marked "baseline": true.
"""
from __future__ import annotations

import argparse
import datetime as dt
import fnmatch
import json
import os
import re
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
SKILL_ROOT = os.path.dirname(HERE)
FINGERPRINT = os.path.join(SKILL_ROOT, "assets", "fingerprint.sh")
CASES_DIR = os.path.join(HERE, "cases")
FIXTURES = os.path.join(HERE, "fixtures")
RUNS_DIR = os.path.join(HERE, ".runs")
RESULTS_DIR = os.path.join(HERE, "results")


# ------------------------------------------------------------------------------
# Scaffolding
# ------------------------------------------------------------------------------
def sh(cmd: list[str], cwd: str | None = None, env: dict | None = None, timeout: int | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)


def fingerprint(ws: str, files: list[str] | None = None) -> str:
    out = sh(["bash", FINGERPRINT, "--root", ws, *(files or [])])
    if out.returncode != 0:
        raise RuntimeError(f"fingerprint.sh failed: {out.stderr.strip()}")
    return out.stdout.strip()


def scaffold(case: dict, ws: str) -> None:
    src = os.path.join(FIXTURES, case["fixture"])
    if not os.path.isdir(src):
        raise RuntimeError(f"fixture not found: {src}")
    shutil.copytree(src, ws)
    if case.get("plan"):
        plan_src = os.path.join(FIXTURES, "plans", case["plan"] + ".md")
        text = open(plan_src, encoding="utf-8").read()
        text = text.replace("{{FINGERPRINT}}", fingerprint(ws))
        text = text.replace("{{FP_PARSE}}", fingerprint(ws, ["src/parse.js"]))
        dst = os.path.join(ws, "docs", "testing", "test-plan.md")
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        open(dst, "w", encoding="utf-8").write(text)
    if case.get("git_init"):
        for cmd in (
            ["git", "init", "-q"],
            ["git", "config", "user.email", "evals@example.invalid"],
            ["git", "config", "user.name", "evals"],
            ["git", "add", "-A"],
            ["git", "commit", "-qm", "fixture"],
        ):
            out = sh(cmd, cwd=ws)
            if out.returncode != 0:
                raise RuntimeError(f"{' '.join(cmd)} failed: {out.stderr.strip()}")
    for m in case.get("mutate_after_baseline", []) or []:
        path = os.path.join(ws, m["file"])
        text = open(path, encoding="utf-8").read()
        if m["find"] not in text:
            raise RuntimeError(f"mutation target not found in {m['file']}: {m['find']!r}")
        open(path, "w", encoding="utf-8").write(text.replace(m["find"], m["replace"], 1))


# ------------------------------------------------------------------------------
# Running claude
# ------------------------------------------------------------------------------
def baseline_config_dir(run_dir: str) -> str:
    cfg = os.path.join(run_dir, "config-without")
    os.makedirs(cfg, exist_ok=True)
    creds = os.path.expanduser("~/.claude/.credentials.json")
    if os.path.isfile(creds):
        shutil.copy2(creds, os.path.join(cfg, ".credentials.json"))
    return cfg


def run_claude(case: dict, ws: str, arm: str, run_dir: str, model: str) -> dict:
    cmd = [
        "claude", "-p", case["prompt"],
        "--output-format", "stream-json", "--verbose",
        "--max-turns", str(case.get("max_turns", 40)),
        "--permission-mode", "bypassPermissions",
        "--model", model,
    ]
    env = dict(os.environ)
    if arm == "without":
        env["CLAUDE_CONFIG_DIR"] = baseline_config_dir(run_dir)
    started = time.time()
    try:
        proc = subprocess.run(cmd, cwd=ws, env=env, capture_output=True, text=True,
                              timeout=case.get("timeout_seconds", 1200))
        timed_out = False
        stdout, stderr, rc = proc.stdout, proc.stderr, proc.returncode
    except subprocess.TimeoutExpired as exc:
        timed_out = True
        stdout = (exc.stdout or b"").decode() if isinstance(exc.stdout, bytes) else (exc.stdout or "")
        stderr = (exc.stderr or b"").decode() if isinstance(exc.stderr, bytes) else (exc.stderr or "")
        rc = -1
    seconds = time.time() - started
    open(os.path.join(run_dir, "stream.jsonl"), "w", encoding="utf-8").write(stdout)
    open(os.path.join(run_dir, "stderr.txt"), "w", encoding="utf-8").write(stderr)
    return {**parse_stream(stdout), "exit_code": rc, "timed_out": timed_out, "seconds": round(seconds, 1)}


def parse_stream(stdout: str) -> dict:
    result_text = ""
    tool_inputs: list[str] = []
    cost = None
    turns = None
    texts: list[str] = []
    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        et = ev.get("type")
        if et == "assistant":
            for block in (ev.get("message") or {}).get("content", []) or []:
                if block.get("type") == "tool_use":
                    tool_inputs.append(json.dumps({"name": block.get("name"), "input": block.get("input")}, ensure_ascii=False))
                elif block.get("type") == "text":
                    texts.append(block.get("text", ""))
        elif et == "result":
            result_text = ev.get("result") or ""
            cost = ev.get("total_cost_usd", cost)
            turns = ev.get("num_turns", turns)
    if not result_text and texts:
        result_text = texts[-1]
    return {"output": result_text, "tool_inputs": tool_inputs, "cost_usd": cost, "turns": turns}


# ------------------------------------------------------------------------------
# Graders
# ------------------------------------------------------------------------------
def read(ws: str, rel: str) -> str | None:
    p = os.path.join(ws, rel)
    return open(p, encoding="utf-8").read() if os.path.isfile(p) else None


def section_text(text: str, section: str) -> str:
    m = re.search(r"^##\s+" + re.escape(section) + r"\s*$", text, re.M)
    if not m:
        return ""
    rest = text[m.end():]
    n = re.search(r"^##\s+", rest, re.M)
    return rest[: n.start()] if n else rest


def table_rows(block: str) -> list[list[str]]:
    rows = []
    for line in block.splitlines():
        s = line.strip()
        if not s.startswith("|"):
            continue
        cells = [c.strip() for c in s.strip("|").split("|")]
        if all(re.fullmatch(r":?-{3,}:?", c) for c in cells if c) and any(cells):
            continue  # separator
        rows.append(cells)
    return rows


PLACEHOLDER = re.compile(r"^(|-|—|n/?a|none|\(none\)|tbd)$", re.I)


def is_placeholder(row: list[str]) -> bool:
    """A row whose id cell is empty or a 'no rows yet' marker is not data."""
    return not row or PLACEHOLDER.match(row[0].strip()) is not None


def data_rows(block: str) -> list[list[str]]:
    rows = table_rows(block)
    return [r for r in rows[1:] if not is_placeholder(r)] if rows else []


def grade(g: dict, ws: str, run: dict) -> tuple[bool, str]:
    t = g["type"]
    out = run.get("output", "") or ""
    if t == "file_exists":
        ok = os.path.isfile(os.path.join(ws, g["path"]))
        return ok, g["path"]
    if t == "file_not_exists":
        ok = not os.path.exists(os.path.join(ws, g["path"]))
        return ok, g["path"]
    if t in ("file_regex", "file_regex_count"):
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        if g.get("section"):
            text = section_text(text, g["section"])
        hits = re.findall(g["pattern"], text, re.M)
        if t == "file_regex":
            return bool(hits), f"{len(hits)} match(es)"
        return len(hits) == g["count"], f"{len(hits)} match(es), expected {g['count']}"
    if t == "section_rows":
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        n = len(data_rows(section_text(text, g["section"])))
        ok = n >= g.get("min", 0) and n <= g.get("max", 10**9)
        return ok, f"{n} row(s)"
    if t == "ranked_rows_equal":
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        n = len(data_rows(section_text(text, "Ranked targets")))
        return n == g["count"], f"{n} row(s), expected {g['count']}"
    if t == "status_changed":
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        rows = data_rows(section_text(text, "Ranked targets"))
        changed = [r for r in rows if r and not re.search(r"(?i)\bpending\b", r[-1])]
        return bool(changed), f"{len(changed)} row(s) no longer pending"
    if t == "findings_have_evidence":
        # The invariant is linkage, not a naming convention: every finding cites an id that
        # actually exists as a row in the Evidence ledger.
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        rows = data_rows(section_text(text, "Findings"))
        ledger_ids = {r[0].strip().strip("`") for r in data_rows(section_text(text, "Evidence ledger"))}
        missing, dangling = [], []
        for r in rows:
            cell = r[4].strip() if len(r) > 4 else ""
            if not cell or PLACEHOLDER.match(cell):
                missing.append(r[0])
                continue
            cited = {c.strip().strip("`") for c in re.split(r"[,;/]| and ", cell) if c.strip()}
            if not (cited & ledger_ids):  # an empty ledger makes every citation dangling
                dangling.append(r[0])
        bad = missing + dangling
        return not bad, f"{len(rows)} finding(s), {len(missing)} without an id, {len(dangling)} citing an id absent from the ledger"
    if t == "no_razonado_in_findings":
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        rows = data_rows(section_text(text, "Findings"))
        bad = [r[0] for r in rows if any("razonado" in c.lower() for c in r)]
        return not bad, f"{len(bad)} razonado finding(s)"
    if t == "findings_rows_matching":
        text = read(ws, g["path"])
        if text is None:
            return False, "file missing"
        rows = data_rows(section_text(text, "Findings"))
        n = sum(1 for r in rows if re.search(g["pattern"], " | ".join(r)))
        return n == g["count"], f"{n} matching row(s), expected {g['count']}"
    if t == "output_regex":
        return bool(re.search(g["pattern"], out, re.M)), "matched" if re.search(g["pattern"], out, re.M) else "no match"
    if t == "output_not_regex":
        target = out.strip().splitlines()[-1] if (g.get("scope") == "last_line" and out.strip()) else out
        hit = re.search(g["pattern"], target, re.M)
        return not hit, "unexpected match" if hit else "clean"
    if t == "question_count":
        n = sum(1 for line in out.splitlines() if line.strip().endswith("?"))
        return n == g["count"], f"{n} question line(s), expected {g['count']}"
    if t == "dir_file_count":
        d = os.path.join(ws, g["path"])
        n = len([f for f in os.listdir(d)]) if os.path.isdir(d) else -1
        return n == g["count"], f"{n} file(s), expected {g['count']}"
    if t == "tool_regex":
        hit = any(re.search(g["pattern"], ti) for ti in run.get("tool_inputs", []))
        return hit, "tool call matched" if hit else "no tool call matched"
    if t == "tool_not_regex":
        hit = any(re.search(g["pattern"], ti) for ti in run.get("tool_inputs", []))
        return not hit, "unexpected tool call" if hit else "clean"
    return False, f"unknown grader type {t}"


def grader_name(g: dict) -> str:
    return g.get("name") or f"{g['type']}:{g.get('pattern') or g.get('section') or g.get('path') or g.get('count','')}"


# ------------------------------------------------------------------------------
# Main
# ------------------------------------------------------------------------------
def load_cases(pattern: str) -> list[dict]:
    cases = []
    for fn in sorted(os.listdir(CASES_DIR)):
        if not fn.endswith(".json"):
            continue
        name = fn[:-5]
        if fnmatch.fnmatch(name, pattern):
            cases.append(json.load(open(os.path.join(CASES_DIR, fn), encoding="utf-8")))
    return cases


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--case", default="*", help="case name glob (default: all)")
    ap.add_argument("--runs", type=int, default=1)
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--arm", choices=["with", "without", "both"], default="with")
    ap.add_argument("--max-cost-usd", type=float, default=None)
    ap.add_argument("--threshold", type=float, default=1.0)
    ap.add_argument("--keep", action="store_true", help="keep workspaces (default: keep on failure only)")
    ap.add_argument("--dry-run", action="store_true", help="scaffold every run and skip claude")
    ap.add_argument("--skip-selftest", action="store_true", help="skip the grader self-test (not recommended)")
    args = ap.parse_args()

    cases = load_cases(args.case)
    if not cases:
        sys.exit(f"no cases match {args.case!r}")
    stamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    # A grader that cannot fail makes every pass meaningless, so prove they can before spending a
    # single model call. Skipped only for --dry-run, which spends nothing.
    if not args.dry_run and not args.skip_selftest:
        st = sh([sys.executable, os.path.join(HERE, "selftest.py")])
        if st.returncode != 0:
            print(st.stdout or st.stderr)
            sys.exit("REFUSED: grader self-test failed; fix the graders before measuring anything.")

    results_dir = os.path.join(RESULTS_DIR, stamp)
    os.makedirs(results_dir, exist_ok=True)
    report: dict = {"timestamp": stamp, "model": args.model, "dry_run": args.dry_run, "cases": {}}
    spent = 0.0
    failed_threshold = False
    lines = ["| case | arm | score | cost USD | turns | minutes | failed graders |", "|---|---|---|---|---|---|---|"]

    for case in cases:
        arms = ["with"] if args.arm == "with" else ["without"] if args.arm == "without" else (["with", "without"] if case.get("baseline") else ["with"])
        report["cases"][case["name"]] = {"arms": {}}
        for arm in arms:
            runs_out = []
            for n in range(1, args.runs + 1):
                run_dir = os.path.join(RUNS_DIR, stamp, case["name"], arm, str(n))
                ws = os.path.join(run_dir, "ws")
                os.makedirs(run_dir, exist_ok=True)
                scaffold(case, ws)
                if args.dry_run:
                    tests = sh(["node", "--test"], cwd=ws if case["fixture"] == "miniapp" else os.path.join(ws, "apps", "alpha"))
                    ok = tests.returncode == 0
                    runs_out.append({"dry_run": True, "workspace": ws, "fixture_tests_green": ok})
                    print(f"[dry-run] {case['name']} [{arm}] #{n}: workspace {ws} scaffolded; fixture tests {'green' if ok else 'RED'}")
                    continue
                if args.max_cost_usd is not None and spent >= args.max_cost_usd:
                    runs_out.append({"skipped": "cost ceiling reached"})
                    print(f"[skip] {case['name']} [{arm}] #{n}: cost ceiling {args.max_cost_usd} reached")
                    continue
                print(f"[run] {case['name']} [{arm}] #{n} ...", flush=True)
                run = run_claude(case, ws, arm, run_dir, args.model)
                spent += run.get("cost_usd") or 0.0
                graded = []
                for g in case["graders"]:
                    ok, detail = grade(g, ws, run)
                    graded.append({"name": grader_name(g), "type": g["type"], "pass": ok, "detail": detail})
                passed = sum(1 for g in graded if g["pass"])
                score = passed / len(graded) if graded else 0.0
                run_record = {
                    "score": round(score, 3), "graders": graded, "cost_usd": run.get("cost_usd"),
                    "turns": run.get("turns"), "seconds": run.get("seconds"), "exit_code": run.get("exit_code"),
                    "timed_out": run.get("timed_out"), "workspace": ws, "output_tail": (run.get("output") or "")[-600:],
                }
                runs_out.append(run_record)
                open(os.path.join(run_dir, "output.md"), "w", encoding="utf-8").write(run.get("output") or "")
                print(f"      score {score:.2f}  cost ${run.get('cost_usd') or 0:.3f}  turns {run.get('turns')}  {run.get('seconds')}s", flush=True)
                if score >= 1.0 and not args.keep:
                    shutil.rmtree(ws, ignore_errors=True)
            scored = [r for r in runs_out if "score" in r]
            case_score = round(sum(r["score"] for r in scored) / len(scored), 3) if scored else None
            report["cases"][case["name"]]["arms"][arm] = {"score": case_score, "runs": runs_out}
            if scored:
                cost = sum(r.get("cost_usd") or 0 for r in scored)
                turns = sum(r.get("turns") or 0 for r in scored) / len(scored)
                minutes = sum(r.get("seconds") or 0 for r in scored) / len(scored) / 60
                failed = sorted({g["name"] for r in scored for g in r["graders"] if not g["pass"]})
                lines.append(f"| {case['name']} | {arm} | {case_score:.2f} | {cost:.3f} | {turns:.0f} | {minutes:.1f} | {', '.join(failed) or '—'} |")
                if arm == "with" and case_score < args.threshold:
                    failed_threshold = True

    report["total_cost_usd"] = round(spent, 4)
    with open(os.path.join(results_dir, "aggregate-result.json"), "w", encoding="utf-8") as fh:
        json.dump(report, fh, indent=2, ensure_ascii=False)
    with_scores = {c: a["arms"].get("with", {}).get("score") for c, a in report["cases"].items()}
    without_scores = {c: a["arms"].get("without", {}).get("score") for c, a in report["cases"].items()}
    delta_lines = []
    for c in report["cases"]:
        if with_scores.get(c) is not None and without_scores.get(c) is not None:
            delta_lines.append(f"- {c}: with {with_scores[c]:.2f} vs without {without_scores[c]:.2f} (delta {with_scores[c]-without_scores[c]:+.2f})")
    summary = "\n".join([
        f"# Eval summary {stamp}", "", f"Model: {args.model} · runs per case: {args.runs} · total cost: ${spent:.3f}", "",
        *lines, "", "## Delta with vs without the skill", "", *(delta_lines or ["(no baseline arm in this run)"]), "",
    ])
    open(os.path.join(results_dir, "summary.md"), "w", encoding="utf-8").write(summary)
    print(f"\nresults: {results_dir}/summary.md")
    if not args.dry_run:
        print(summary)
    sys.exit(1 if failed_threshold else 0)


if __name__ == "__main__":
    main()
