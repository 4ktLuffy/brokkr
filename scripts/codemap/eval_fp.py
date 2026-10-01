#!/usr/bin/python3
"""False-positive rate of the static check (internal/codemap/check.py).

For every task whose gold patch touches .py files, build the patched version of
each touched file (the pre-image from the task repo plus the gold patch, applied
in a scratch directory) and run the check on it. The gold patches are known
good, so every report is a false positive unless it is genuinely in the
original code. Two numbers per file: all reports in the patched file, and the
reports the agent would actually see (those not already in the pre-image).

Usage: eval_fp.py TASKS_DIR [TASKS_DIR...]     (each holds <id>/repo and <id>/patches/good.patch)
Run with the system python3 (the interpreter brokkr fix uses). Nothing in the
repositories is imported or executed.
"""
import os
import re
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "internal", "codemap"))
import check  # noqa: E402


def files_in(patch):
    return re.findall(r"^diff --git a/(\S+) b/(\S+)$", patch, re.M)


def main():
    tasks = 0
    py_files = 0
    raw_reports = []
    seen_reports = []
    pre_reports = 0
    for tdir in sys.argv[1:]:
        for tid in sorted(os.listdir(tdir)):
            gp = os.path.join(tdir, tid, "patches", "good.patch")
            repo = os.path.join(tdir, tid, "repo")
            if not os.path.exists(gp):
                continue
            patch = open(gp, encoding="utf-8", errors="replace").read()
            pys = [b for a, b in files_in(patch) if b.endswith(".py")]
            if not pys:
                continue
            tmp = tempfile.mkdtemp()
            try:
                for rel in pys:
                    src = os.path.join(repo, rel)
                    if os.path.exists(src):
                        os.makedirs(os.path.dirname(os.path.join(tmp, rel)), exist_ok=True)
                        shutil.copy(src, os.path.join(tmp, rel))
                r = subprocess.run(["git", "apply", "-p1", "--include=*.py", "--whitespace=nowarn", gp],
                                   cwd=tmp, capture_output=True, text=True)
                if r.returncode != 0:
                    print("SKIP %s: patch did not apply: %s" % (tid, r.stderr.strip()[:100]))
                    continue
                tasks += 1
                for rel in pys:
                    new = os.path.join(tmp, rel)
                    if not os.path.exists(new):
                        continue
                    py_files += 1
                    pnew = check.check(open(new, "rb").read(), rel)
                    old = os.path.join(repo, rel)
                    pold = check.check(open(old, "rb").read(), rel) if os.path.exists(old) else []
                    pre_reports += len(pold)
                    for p in pnew:
                        raw_reports.append("%s %s:%d %s" % (tid, rel, p[0], p[3]))
                    for p in check.new_problems(pnew, pold):
                        seen_reports.append("%s %s:%d %s" % (tid, rel, p[0], p[3]))
            finally:
                shutil.rmtree(tmp, ignore_errors=True)
    print("tasks with a patched .py file: %d; patched .py files checked: %d" % (tasks, py_files))
    print("reports in the pre-image files (already there before the patch): %d" % pre_reports)
    print("reports in the patched files: %d" % len(raw_reports))
    print("reports the agent would see (introduced by the patch): %d" % len(seen_reports))
    for l in raw_reports:
        print("  RAW  " + l)
    for l in seen_reports:
        print("  SEEN " + l)


if __name__ == "__main__":
    main()
