#!/usr/bin/env python3
"""Fill required_tests for tasks built by scripts/gh/prepare.py, the way
SWE-bench's harness defines them. Runs in the Lima VM with the system Python.

    /usr/bin/python3 scripts/gh/derive_tests.py TASK_DIR [TASK_DIR ...]

For each task, two sandboxed runs through `brokkr verify` (the hidden test
patch is always applied by the verifier):

  A: the base commit, no fix     B: the base commit plus the gold fix

FAIL_TO_PASS = passing in B and not passing in A
PASS_TO_PASS = passing in both

A task with no FAIL_TO_PASS test is marked unusable: its fix is not tested by
anything the patch adds or changes. The counts and the runs used are recorded
in task.json under "derived".
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

BROKKR = os.environ.get("BROKKR_BIN", str(Path.home() / ".cache/brokkr/bin/brokkr-v0.4"))
RUNS = Path(os.environ.get("BROKKR_CACHE", Path.home() / ".cache/brokkr")) / "runs" / "derive"


def run(task: Path, patch: str | None, out: Path) -> dict:
    args = [BROKKR, "verify", "--collect-only", "--task", str(task / "task.json"), "--repo", str(task / "repo"), "--out", str(out)]
    if patch:
        args += ["--patch", patch]
    subprocess.run(args, capture_output=True, text=True)
    ev = json.loads((out / "evidence.json").read_text())
    if ev["verdict"] != "COLLECTED":
        raise RuntimeError(f"{ev['verdict']}: {ev['reasons']}")
    return ev


def main() -> None:
    for d in map(Path, sys.argv[1:]):
        t = json.loads((d / "task.json").read_text())
        name = t["name"]
        try:
            a = run(d, None, RUNS / name / "A")
            b = run(d, str(d / "patches/good.patch"), RUNS / name / "B")
        except Exception as e:  # noqa: BLE001 - record and move on
            t["derived"] = {"usable": False, "reason": str(e)[:300]}
            (d / "task.json").write_text(json.dumps(t, indent=2) + "\n")
            print(f"{name}: ERROR {e}", flush=True)
            continue
        pa, pb = set(a["tests"]["passed"]), set(b["tests"]["passed"])
        f2p = sorted(pb - pa)
        p2p = sorted(pa & pb)
        broke = sorted(pa - pb)  # passing before, not after the gold fix
        usable = bool(f2p)
        t["required_tests"] = f2p + p2p
        t["derived"] = {
            "usable": usable, "fail_to_pass": len(f2p), "pass_to_pass": len(p2p),
            "broken_by_gold": broke[:20], "runs": {"A": a["run_id"], "B": b["run_id"]},
            "reason": None if usable else "no test fails before the fix and passes after it",
        }
        (d / "task.json").write_text(json.dumps(t, indent=2) + "\n")
        print(f"{name}: F2P {len(f2p)} P2P {len(p2p)} broken-by-gold {len(broke)} -> {'usable' if usable else 'UNUSABLE'}", flush=True)


if __name__ == "__main__":
    main()
