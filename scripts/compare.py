#!/usr/bin/env python3
"""Paired comparison of two runs on the same tasks.

    python3 scripts/compare.py "A=dir1,dir2" "B=dir3,dir4"

Only tasks scored in both runs are compared (infra rows excluded, as in
report.py). For each, the verdicts pair up: both PASS, only A, only B, neither.
The question "is one better?" is decided by the discordant pairs alone, with an
exact two-sided McNemar (binomial) test: with few tasks, a difference of one or
two is usually noise, and this says so.
"""
from __future__ import annotations

import json
import sys
from math import comb
from pathlib import Path


def rows(spec: str) -> tuple[str, dict[str, bool]]:
    label, _, dirs = spec.partition("=")
    out: dict[str, bool] = {}
    for d in dirs.split(","):
        if not d:
            continue
        for line in (Path(d) / "runs.jsonl").read_text().splitlines():
            r = json.loads(line)
            if r.get("invalid") or r.get("verdict") == "ERROR" or r.get("infra_error") or len(r.get("served_by") or {}) > 1:
                continue
            if r["task"] in out:
                raise SystemExit(f"{label}: {r['task']} scored twice")
            out[r["task"]] = r.get("verdict") == "PASS"
    return label, out


def mcnemar_exact(b: int, c: int) -> float:
    """Two-sided exact p-value for b vs c discordant pairs under p = 1/2."""
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    tail = sum(comb(n, i) for i in range(k + 1)) / 2**n
    return min(1.0, 2 * tail)


def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    (la, a), (lb, b) = rows(sys.argv[1]), rows(sys.argv[2])
    common = sorted(set(a) & set(b))
    both = sum(a[t] and b[t] for t in common)
    only_a = [t for t in common if a[t] and not b[t]]
    only_b = [t for t in common if b[t] and not a[t]]
    neither = len(common) - both - len(only_a) - len(only_b)
    p = mcnemar_exact(len(only_a), len(only_b))
    print(f"Paired on {len(common)} tasks scored in both runs.\n")
    print("| | count |\n|---|---|")
    print(f"| both PASS | {both} |")
    print(f"| only {la} | {len(only_a)} |")
    print(f"| only {lb} | {len(only_b)} |")
    print(f"| neither | {neither} |")
    print(f"\n{la}: {both + len(only_a)}/{len(common)}; {lb}: {both + len(only_b)}/{len(common)}.")
    print(f"Exact McNemar test on the {len(only_a) + len(only_b)} discordant tasks: p = {p:.3f}"
          + (" (not distinguishable from chance)" if p >= 0.05 else ""))
    if only_a:
        print(f"\nonly {la}: " + ", ".join(only_a))
    if only_b:
        print(f"only {lb}: " + ", ".join(only_b))


if __name__ == "__main__":
    main()
