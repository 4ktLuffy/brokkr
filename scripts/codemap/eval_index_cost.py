#!/usr/bin/python3
"""Index build time and memory per repository (what `brokkr fix --code-tools` pays once per run).

Usage: eval_index_cost.py REPO_DIR [REPO_DIR...]   (each in a fresh process is best: peak RSS is per process)
"""
import os
import resource
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "internal", "codemap"))
import index  # noqa: E402

for root in sys.argv[1:]:
    ix = index.Index(root)
    rss0 = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024.0
    t = time.time()
    ix.refresh()
    build = time.time() - t
    rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024.0
    t = time.time()
    ix.refresh()
    idle = time.time() - t
    victim = next(r for r in ix.files if r.endswith(".py"))
    p = os.path.join(root, victim)
    st = os.stat(p)
    os.utime(p, ns=(st.st_atime_ns, st.st_mtime_ns + 10**9))
    t = time.time()
    ix.refresh()
    edit = time.time() - t
    os.utime(p, ns=(st.st_atime_ns, st.st_mtime_ns))
    t = time.time()
    ix.find_usages("get")
    q1 = time.time() - t
    t = time.time()
    ix.find_definition("get")
    q2 = time.time() - t
    s = ix.stats()
    print("%s: %d files (%d unparsed), %d defs, %d refs; build %.2fs; peak RSS %.0f MB (interpreter alone %.0f MB); "
          "refresh with no change %.0f ms, after one edit %.0f ms; find_usages('get') %.0f ms, find_definition('get') %.0f ms"
          % (os.path.basename(os.path.dirname(root)), s["files"], s["unparsed"], s["defs"], s["refs"], build, rss, rss0,
             idle * 1000, edit * 1000, q1 * 1000, q2 * 1000))
