#!/usr/bin/env python3
"""Cross-check Brokkr's Go ports of SWE-bench's log parsers against the
originals. Runs SWE-bench v4.1.0's parse_log_<parser> (fetched at that tag;
the parser is the one named in the evidence's task: django, sympy or pytest) on the
same stdout+stderr Brokkr parsed, and compares the sets of passed and failed
test names with those in Brokkr's evidence.json.

    python3 scripts/swe/crosscheck_parser.py RUN_DIR [RUN_DIR ...]

Exit status 1 if any run disagrees.
"""
import enum
import json
import re
import sys
import types
import urllib.request
from pathlib import Path

URL = "https://raw.githubusercontent.com/SWE-bench/SWE-bench/v4.1.0/swebench/harness/log_parsers/python.py"
src = urllib.request.urlopen(URL, timeout=60).read().decode()


class TestStatus(enum.Enum):
    FAILED = "FAILED"
    PASSED = "PASSED"
    SKIPPED = "SKIPPED"
    ERROR = "ERROR"
    XFAIL = "XFAIL"


def original(name: str):
    start = src.index(f"def parse_log_{name}(")
    end = src.index("\ndef ", start + 1)
    ns = {"re": re, "TestStatus": TestStatus, "TestSpec": object}
    exec(src[start:end], ns)
    return ns[f"parse_log_{name}"]


parsers = {}

bad = 0
for run in map(Path, sys.argv[1:]):
    log = (run / "stdout.log").read_text(errors="replace") + (run / "stderr.log").read_text(errors="replace")
    ev = json.loads((run / "evidence.json").read_text())
    name = ev["task"].get("parser") or "django"
    if name not in parsers:
        parsers[name] = original(name)
    ref = parsers[name](log, None)
    # SWE-bench's grading counts XFAIL as passing (only pytest logs have it).
    ref_pass = {k for k, v in ref.items() if v in ("PASSED", "XFAIL")}
    ref_fail = {k for k, v in ref.items() if v in ("FAILED", "ERROR")}
    ours_pass, ours_fail = set(ev["tests"]["passed"]), set(ev["tests"]["failed"])
    same = ref_pass == ours_pass and ref_fail == ours_fail
    bad += not same
    print(f"{run.name}: swebench passed={len(ref_pass)} failed={len(ref_fail)} | brokkr passed={len(ours_pass)} failed={len(ours_fail)} -> {'SAME' if same else 'DIFFERENT'}")
    if not same:
        for label, a, b in (("passed", ref_pass, ours_pass), ("failed", ref_fail, ours_fail)):
            for k in sorted(a - b)[:5]:
                print(f"   only swebench {label}: {k}")
            for k in sorted(b - a)[:5]:
                print(f"   only brokkr {label}: {k}")
sys.exit(1 if bad else 0)
