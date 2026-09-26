#!/usr/bin/env python3
"""Cross-check Brokkr's Go port of SWE-bench's Django log parser against the
original. Runs SWE-bench v4.1.0's parse_log_django (fetched at that tag) on the
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
start = src.index("def parse_log_django(")
end = src.index("\ndef ", start + 1)


class TestStatus(enum.Enum):
    FAILED = "FAILED"
    PASSED = "PASSED"
    SKIPPED = "SKIPPED"
    ERROR = "ERROR"
    XFAIL = "XFAIL"


ns = {"re": re, "TestStatus": TestStatus, "TestSpec": object}
exec(src[start:end], ns)
parse = ns["parse_log_django"]

bad = 0
for run in map(Path, sys.argv[1:]):
    log = (run / "stdout.log").read_text(errors="replace") + (run / "stderr.log").read_text(errors="replace")
    ref = parse(log, None)
    ref_pass = {k for k, v in ref.items() if v == "PASSED"}
    ref_fail = {k for k, v in ref.items() if v in ("FAILED", "ERROR")}
    ev = json.loads((run / "evidence.json").read_text())
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
