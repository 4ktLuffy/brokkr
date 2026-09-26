#!/usr/bin/env python3
"""Combine eval result directories into one comparison.

    python3 scripts/report.py results/<run-a> results/<run-b> [...] [--split results/swe/split.json]

Reads each directory's runs.jsonl (written by scripts/eval.sh). Refuses to pool
runs from different harness versions into one column: a column is one model at
one harness version. Infra errors are listed and never scored. When a split
file is given, tasks are labelled dev or held-out, and the headline counts only
held-out tasks.
"""
from __future__ import annotations

import argparse
import collections
import json
from pathlib import Path


def load(d: Path) -> tuple[str, list[dict]]:
    rows = [json.loads(l) for l in (d / "runs.jsonl").read_text().splitlines() if l.strip()]
    rows = [r for r in rows if not r.get("invalid")]
    harness = {r.get("harness", "?") for r in rows if r.get("verdict") != "ERROR" or r.get("harness")}
    if len(harness) > 1:
        raise SystemExit(f"{d}: runs from several harness versions {sorted(harness)}; not pooling them")
    model = next((r.get("model") for r in rows if r.get("model")), d.name)
    label = f"{model} (h{next(iter(harness), '?')})"
    return label, rows


def is_infra(r: dict) -> bool:
    return bool(r.get("infra_error")) or r.get("verdict") == "ERROR"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("dirs", nargs="+", type=Path)
    ap.add_argument("--split", type=Path)
    ap.add_argument("--difficulty-from", type=Path, help="directory of prepared tasks (task.json has swebench.difficulty)")
    args = ap.parse_args()

    split = json.loads(args.split.read_text()) if args.split else None
    part = {}
    if split:
        part.update({t: "dev" for t in split["dev"]})
        part.update({t: "held-out" for t in split["test"]})
    diff = {}
    if args.difficulty_from:
        for tj in args.difficulty_from.glob("*/task.json"):
            j = json.loads(tj.read_text())
            diff[j["name"]] = j.get("swebench", {}).get("difficulty", "?")

    cols = [load(d) for d in args.dirs]
    tasks = sorted({r["task"] for _, rows in cols for r in rows})
    out = ["| Task | Set | Difficulty | " + " | ".join(l for l, _ in cols) + " |",
           "|---|---|---|" + "---|" * len(cols)]
    for t in tasks:
        cells = []
        for _, rows in cols:
            rs = [r for r in rows if r["task"] == t]
            if not rs:
                cells.append("–")
                continue
            marks = []
            for r in rs:
                if is_infra(r):
                    marks.append("infra")
                elif r.get("verdict") == "PASS":
                    marks.append("PASS")
                else:
                    marks.append("fail" + ("*" if r.get("agent_claimed_fixed") else ""))
            cells.append(" ".join(marks))
        out.append(f"| {t} | {part.get(t, '')} | {diff.get(t, '')} | " + " | ".join(cells) + " |")

    out += ["", "`fail*` = the agent claimed it had fixed the task and the hidden tests disagreed (over-claim).", ""]
    for subset in (["held-out"], ["dev"], ["dev", "held-out"]) if split else ([None],):
        name = "+".join(subset) if subset[0] else "all"
        out.append(f"**{name}**")
        out.append("")
        out.append("| Model | Verified PASS / scored | by difficulty | Claimed | Over-claims | Infra (unscored) | Median tokens/run | Median min/run |")
        out.append("|---|---|---|---|---|---|---|---|")
        for label, rows in cols:
            rs = [r for r in rows if subset[0] is None or part.get(r["task"]) in subset]
            scored = [r for r in rs if not is_infra(r)]
            p = sum(r.get("verdict") == "PASS" for r in scored)
            c = sum(bool(r.get("agent_claimed_fixed")) for r in scored)
            o = sum(bool(r.get("agent_claimed_fixed")) and r.get("verdict") != "PASS" for r in scored)
            byd = collections.defaultdict(lambda: [0, 0])
            for r in scored:
                byd[diff.get(r["task"], "?")][1] += 1
                byd[diff.get(r["task"], "?")][0] += r.get("verdict") == "PASS"
            bd = ", ".join(f"{k}: {v[0]}/{v[1]}" for k, v in sorted(byd.items()))
            tok = sorted(r.get("prompt_tokens", 0) + r.get("completion_tokens", 0) for r in scored)
            mins = sorted(r.get("wall_ms", 0) / 60000 for r in scored)
            med = lambda xs: xs[len(xs) // 2] if xs else 0
            pct = f" ({100 * p / len(scored):.0f}%)" if scored else ""
            out.append(f"| {label} | {p}/{len(scored)}{pct} | {bd} | {c} | {o} | {len(rs) - len(scored)} | {med(tok):,} | {med(mins):.1f} |")
        out.append("")
    print("\n".join(out))


if __name__ == "__main__":
    main()
