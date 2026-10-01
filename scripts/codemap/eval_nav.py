#!/usr/bin/python3
"""Offline evaluation of find_definition / find_usages against gold patches. No model.

For each task: take the gold patch (patches/good.patch), find the functions its
changed lines are in (deleted lines in the original file, added lines in the
patched file), then ask the tools the questions an agent would ask:

 (a) find_definition: is each modified function found, asked by its qualified
     name (Class.method) and by its bare name (what a traceback or issue gives)?
 (b) find_usages: in fixes that change several files, does find_usages(f) for a
     function f changed in one file point at another file the patch changes?
     "file" = that file appears in the listing; "function" = a usage or
     same-name definition falls inside a function the gold patch modifies there.
     Both are computed on the output the model would see (capped) and uncapped.
 (c) index build time and memory, measured per repository.

Usage: eval_nav.py [--all] [--per-family N] TASKS_DIR [TASKS_DIR...]
Run with the system python3. Only parses; nothing in a repository is executed.
"""
import ast
import os
import re
import resource
import shutil
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "internal", "codemap"))
import index  # noqa: E402


def is_test(rel):
    return index.is_test_path(rel)


def parse_patch(patch):
    """{file: (deleted old line numbers, added new line numbers)} for .py files."""
    out, cur = {}, None
    old = new = 0
    for line in patch.split("\n"):
        m = re.match(r"^diff --git a/(\S+) b/(\S+)$", line)
        if m:
            cur = m.group(2) if m.group(2).endswith(".py") else None
            if cur:
                out[cur] = ([], [])
            continue
        m = re.match(r"^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@", line)
        if m:
            old, new = int(m.group(1)), int(m.group(2))
            continue
        if cur is None or not (line[:1] in "+- " ) or line.startswith(("+++", "---")):
            continue
        if line.startswith("-"):
            out[cur][0].append(old)
            old += 1
        elif line.startswith("+"):
            out[cur][1].append(new)
            new += 1
        else:
            old += 1
            new += 1
    return out


def indexed_defs(src):
    """qualname -> (start, end) for what the index indexes: module/class-level functions, methods, classes."""
    try:
        tree = ast.parse(src)
    except (SyntaxError, ValueError):
        return {}
    defs = []
    index.collect_defs(tree.body, "", defs, False)
    return {d[1]: (d[3], d[4], d[2], d[0]) for d in defs if d[2] in ("function", "method")}


def enclosing(defs, line):
    best = None
    for q, (s, e, kind, simple) in defs.items():
        if s <= line <= e and (best is None or s >= defs[best][0]):
            best = q
    return best


def shown_files(text):
    """Files and (file, line) pairs a find_usages answer lists, including same-name definitions."""
    files, lines = set(), set()
    cur = None
    for l in text.split("\n"):
        m = re.match(r"^(\S+\.py) \(\d+\)$", l)
        if m:
            cur = m.group(1)
            files.add(cur)
            continue
        m = re.match(r"^  (\d+): \[", l)
        if m and cur:
            lines.add((cur, int(m.group(1))))
        if l.startswith("Also defined"):
            for f, n in re.findall(r"(\S+\.py):(\d+) ", l):
                files.add(f)
                lines.add((f, int(n)))
    return files, lines


def main():
    args = sys.argv[1:]
    use_all = "--all" in args
    per = 14
    if "--per-family" in args:
        per = int(args[args.index("--per-family") + 1])
    dirs = [a for a in args if os.path.isdir(a)]
    tasks = []
    for d in dirs:
        ids = sorted(t for t in os.listdir(d) if os.path.exists(os.path.join(d, t, "patches", "good.patch")))
        fam = {}
        for t in ids:
            fam.setdefault(t.split("__")[0], []).append(t)
        for f, lst in fam.items():
            if not use_all and len(lst) > per:
                lst = [lst[int(i * len(lst) / per)] for i in range(per)]
            tasks += [(d, t) for t in lst]

    tot = dict(tasks=0, funcs=0, new_funcs=0, nonfunc_tasks=0, q_hit=0, bare_hit=0, bare_rank1=0, bare_nres=[],
               multi=0, multi_loose=0, multi_strict=0, multi_loose_unc=0, multi_strict_unc=0, files_listed=[],
               pairs=0, pairs_loose=0, pairs_strict=0)
    perfam = {}
    rows = []
    for d, tid in tasks:
        repo = os.path.join(d, tid, "repo")
        patch = open(os.path.join(d, tid, "patches", "good.patch"), encoding="utf-8", errors="replace").read()
        changes = parse_patch(patch)
        changes = {f: v for f, v in changes.items() if os.path.exists(os.path.join(repo, f))}
        if not changes:
            continue
        tmp = tempfile.mkdtemp()
        try:
            for rel in changes:
                os.makedirs(os.path.dirname(os.path.join(tmp, rel)), exist_ok=True)
                shutil.copy(os.path.join(repo, rel), os.path.join(tmp, rel))
            r = subprocess.run(["git", "apply", "-p1", "--include=*.py", "--whitespace=nowarn",
                                os.path.join(d, tid, "patches", "good.patch")], cwd=tmp, capture_output=True)
            if r.returncode != 0:
                continue
            ix = index.Index(repo)
            ix.refresh()
            mod = {}   # file -> {qual: (start, end, simple)} existing functions the patch modifies
            nonfunc = False
            nnew = 0
            for rel, (dels, adds) in changes.items():
                base = indexed_defs(open(os.path.join(repo, rel), encoding="utf-8", errors="replace").read())
                patched = indexed_defs(open(os.path.join(tmp, rel), encoding="utf-8", errors="replace").read())
                quals = set()
                for l in dels:
                    q = enclosing(base, l)
                    quals.add(q) if q else None
                    nonfunc |= q is None
                for l in adds:
                    q = enclosing(patched, l)
                    if q is None:
                        nonfunc = True
                    else:
                        quals.add(q)
                for q in quals:
                    if q in base:
                        mod.setdefault(rel, {})[q] = (base[q][0], base[q][1], base[q][3])
                    else:
                        nnew += 1
            nonlib = [f for f in mod if not is_test(f)]
            tot["tasks"] += 1
            tot["new_funcs"] += nnew
            tot["nonfunc_tasks"] += nonfunc
            fam = perfam.setdefault(tid.split("__")[0], dict(tasks=0, multi=0, loose=0, strict=0, funcs=0, qhit=0, bhit=0))
            fam["tasks"] += 1
            # (a)
            for rel, fs in mod.items():
                for q, (s, e, simple) in fs.items():
                    tot["funcs"] += 1
                    fam["funcs"] += 1
                    want = "%s:%d-" % (rel, s)
                    if want in ix.find_definition(q):
                        tot["q_hit"] += 1
                        fam["qhit"] += 1
                    out = ix.find_definition(simple)
                    shown = [l for l in out.split("\n") if re.match(r"^\S+\.py:\d+-\d+  ", l)]
                    tot["bare_nres"].append(len(shown))
                    if want in out:
                        tot["bare_hit"] += 1
                        fam["bhit"] += 1
                        if shown and shown[0].startswith(want):
                            tot["bare_rank1"] += 1
            # (b) multi-file fixes: more than one file changed (any .py file of the gold patch)
            gold_files = sorted(f for f in changes if not is_test(f))
            if len(gold_files) >= 2 and len(mod) >= 1:
                tot["multi"] += 1
                fam["multi"] += 1
                loose = strict = loose_u = strict_u = False
                modranges = {f: [(s, e) for (s, e, _) in fs.values()] for f, fs in mod.items()}
                for rel, fs in mod.items():
                    if is_test(rel):
                        continue
                    for q, (s, e, simple) in fs.items():
                        out = ix.find_usages(simple)
                        files, lines = shown_files(out)
                        tot["files_listed"].append(len(files))
                        for other in gold_files:
                            if other == rel:
                                continue
                            tot["pairs"] += 1
                            lo = other in files
                            st = any(f == other and any(a <= n <= b for a, b in modranges.get(other, [])) for f, n in lines)
                            tot["pairs_loose"] += lo
                            tot["pairs_strict"] += st
                            loose |= lo
                            strict |= st
                        # uncapped: every reference and same-name definition in the index
                        hits = {}
                        for f, fi in ix.files.items():
                            for ref in fi.refs.get(simple, []):
                                hits.setdefault(f, set()).add(ref >> 3)
                            for dd in fi.defs:
                                if dd[0] == simple:
                                    hits.setdefault(f, set()).add(dd[3])
                        for other in gold_files:
                            if other == rel:
                                continue
                            if other in hits:
                                loose_u = True
                                if any(a <= n <= b for n in hits[other] for a, b in modranges.get(other, [])):
                                    strict_u = True
                tot["multi_loose"] += loose
                tot["multi_strict"] += strict
                tot["multi_loose_unc"] += loose_u
                tot["multi_strict_unc"] += strict_u
                fam["loose"] += loose
                fam["strict"] += strict
                rows.append((tid, len(gold_files), loose, strict, loose_u, strict_u))
        finally:
            shutil.rmtree(tmp, ignore_errors=True)

    def pct(a, b):
        return "%d/%d (%.0f%%)" % (a, b, 100.0 * a / b) if b else "n/a"

    print("tasks evaluated: %d; gold-modified existing functions: %d; new functions added by the gold patches: %d; tasks with changes outside any function: %d"
          % (tot["tasks"], tot["funcs"], tot["new_funcs"], tot["nonfunc_tasks"]))
    print("(a) find_definition by qualified name finds the function:   %s" % pct(tot["q_hit"], tot["funcs"]))
    print("(a) find_definition by bare name, within the 15 shown:      %s" % pct(tot["bare_hit"], tot["funcs"]))
    print("(a) ... and as the first result:                            %s" % pct(tot["bare_rank1"], tot["funcs"]))
    n = sorted(tot["bare_nres"])
    if n:
        print("    results shown for a bare name: median %d, max %d" % (n[len(n) // 2], n[-1]))
    print("(b) multi-file fixes: %d. A find_usages of a changed function lists another gold file:" % tot["multi"])
    print("      file level, shown output:      %s" % pct(tot["multi_loose"], tot["multi"]))
    print("      function level, shown output:  %s" % pct(tot["multi_strict"], tot["multi"]))
    print("      file level, uncapped:          %s" % pct(tot["multi_loose_unc"], tot["multi"]))
    print("      function level, uncapped:      %s" % pct(tot["multi_strict_unc"], tot["multi"]))
    fl = tot["files_listed"]
    if fl:
        print("      files listed per query: mean %.1f (so a file-level hit is partly chance); pairs (changed fn, other gold file): file %s, function %s"
              % (sum(fl) / len(fl), pct(tot["pairs_loose"], tot["pairs"]), pct(tot["pairs_strict"], tot["pairs"])))
    for f, v in sorted(perfam.items()):
        print("  %-8s tasks %d, functions %d: qualified %s, bare %s; multi-file %d: file %d, function %d (shown)"
              % (f, v["tasks"], v["funcs"], pct(v["qhit"], v["funcs"]), pct(v["bhit"], v["funcs"]), v["multi"], v["loose"], v["strict"]))
    for tid, nf, lo, st, lou, stu in rows:
        print("  multi %s files=%d file=%d function=%d uncapped file=%d function=%d" % (tid, nf, lo, st, lou, stu))


if __name__ == "__main__":
    main()
