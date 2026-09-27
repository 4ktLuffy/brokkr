#!/usr/bin/env python3
"""Why do agents fail? Classify every scored run from its own evidence.

    python3 scripts/analyze_failures.py [results/*/runs.jsonl ...]   # run inside the Lima VM

Runs inside the Lima VM, where the run directories (transcripts, patches,
evidence) live. For each scored run it reads final.patch, the task's gold
patch and the final evidence, and puts a failure in the first bucket that fits:

  no_change        the agent submitted nothing
  stuck            stopped by loop detection
  out_of_turns     turn budget exhausted
  broke_syntax     the final test run hit SyntaxError/IndentationError
  wrong_location   edited none of the files the real fix edits
  broke_existing   right file(s), but a previously passing (PASS_TO_PASS) test now fails
  incomplete_fix   right file(s), no regressions, but the target (FAIL_TO_PASS) tests still fail

It also records over-claims and whether the agent ran a reproduction.
"""
from __future__ import annotations

import collections
import glob
import json
import re
import sys
from pathlib import Path

TASK_ROOTS = [Path.home() / ".cache/brokkr-swe/tasks", Path.home() / ".cache/brokkr-gh/tasks"]


def patch_files(text: str) -> set[str]:
    out = set()
    for line in text.splitlines():
        m = re.match(r"^\+\+\+ b/(\S+)", line) or re.match(r"^diff -ruN .* a/(\S+) b/\S+", line)
        if m:
            out.add(m.group(1))
    return {f for f in out if f != "/dev/null"}


def task_dir(name: str) -> Path | None:
    for root in TASK_ROOTS:
        if (root / name).exists():
            return root / name
    return None


def classify(row: dict) -> dict | None:
    if row.get("verdict") == "ERROR" or row.get("infra_error") or not row.get("dir"):
        return None
    run = Path(row["dir"])
    td = task_dir(row["task"])
    if td is None:
        return None
    task = json.loads((td / "task.json").read_text())
    gold = patch_files((td / "patches/good.patch").read_text())
    final = (run / "final.patch").read_text() if (run / "final.patch").exists() else ""
    edited = patch_files(final)
    ev_p = run / "final" / "evidence.json"
    ev = json.loads(ev_p.read_text()) if ev_p.exists() else None
    stderr = (run / "final" / "stderr.log").read_text(errors="replace") if (run / "final" / "stderr.log").exists() else ""
    stdout = (run / "final" / "stdout.log").read_text(errors="replace") if (run / "final" / "stdout.log").exists() else ""
    info = {"task": row["task"], "verdict": row["verdict"], "claimed": bool(row.get("agent_claimed_fixed")),
            "reproduced": (row.get("python_runs") or 0) > 0, "turns": row.get("turns", 0),
            "edited_right_file": bool(edited & gold), "gold_files": len(gold)}
    if row["verdict"] == "PASS":
        info["bucket"] = "pass"
        return info
    stop = row.get("stop_reason") or ""
    missing = set(ev["tests"]["missing_required"]) if ev else set()
    # FAIL_TO_PASS tests are the ones the unpatched repo does not pass; the
    # task's derived/SWE-bench counts put them first in required_tests.
    f2p_n = (task.get("derived") or {}).get("fail_to_pass") or (task.get("swebench") or {}).get("fail_to_pass") or 0
    f2p = set(task["required_tests"][:f2p_n]) if f2p_n else set()
    if not final.strip():
        b = "no_change"
    elif stop.startswith("stuck"):
        b = "stuck"
    elif stop == "turn budget exhausted":
        b = "out_of_turns"
    elif re.search(r"\b(SyntaxError|IndentationError)\b", stderr + stdout):
        b = "broke_syntax"
    elif not (edited & gold):
        b = "wrong_location"
    elif missing - f2p:
        b = "broke_existing"
    else:
        b = "incomplete_fix"
    info["bucket"] = b
    return info


def main() -> None:
    files = sys.argv[1:] or glob.glob(str(Path(__file__).resolve().parent.parent / "results/*/runs.jsonl"))
    rows = []
    for f in files:
        for l in open(f):
            r = json.loads(l)
            r["_src"] = Path(f).parent.name
            rows.append(r)
    out = [c for r in rows if (c := classify(r))]
    json.dump(out, sys.stdout if False else open("/tmp/failure-analysis.json", "w"), indent=1)
    by = collections.Counter(c["bucket"] for c in out)
    n_fail = sum(v for k, v in by.items() if k != "pass")
    print(f"scored runs analysed: {len(out)}  (pass {by['pass']}, fail {n_fail})\n")
    print("| Why it failed | runs | share of failures |\n|---|---|---|")
    for k, v in by.most_common():
        if k != "pass":
            print(f"| {k} | {v} | {100 * v / n_fail:.0f}% |")
    rep = [c for c in out if c["reproduced"]]
    norep = [c for c in out if not c["reproduced"]]
    rate = lambda xs: f"{sum(c['bucket'] == 'pass' for c in xs)}/{len(xs)} ({100 * sum(c['bucket'] == 'pass' for c in xs) / max(1, len(xs)):.0f}%)"
    print(f"\npass rate when the agent ran a reproduction: {rate(rep)}; when it did not: {rate(norep)}")
    right = [c for c in out if c["edited_right_file"]]
    print(f"runs that edited at least one file the real fix edits: {len(right)}/{len(out)}; pass rate among them: {rate(right)}")
    oc = [c for c in out if c["claimed"] and c["bucket"] != "pass"]
    print(f"over-claims: {len(oc)} of {sum(c['claimed'] for c in out)} claims; their buckets: {dict(collections.Counter(c['bucket'] for c in oc))}")
    multi = [c for c in out if c["gold_files"] > 1]
    print(f"tasks whose real fix spans >1 file: {len(multi)} runs, pass rate {rate(multi)}; single-file: {rate([c for c in out if c['gold_files'] <= 1])}")


if __name__ == "__main__":
    main()
