#!/usr/bin/env python3
"""Combine eval result directories into one comparison.

    python3 scripts/report.py results/<run-a> results/<run-b> [...] [--split results/swe/split.json]
    python3 scripts/report.py --col "Codestral 0.3.1=results/a,results/b" --col ...

Reads each directory's runs.jsonl (written by scripts/eval.sh). A column is one
directory, or with --col several directories merged (a run resumed across
nights, or split into shards). Refuses to pool runs from different harness
versions into one column: a column is one model at one harness version. Infra
errors are listed and never scored; if a task has a scored row, its infra rows
are dropped (the task was rerun). Two scored rows for one task in one column
is an error, not a choice. When a split file is given, tasks are labelled dev
or held-out, and the headline counts only held-out tasks.
"""
from __future__ import annotations

import argparse
import collections
import json
from pathlib import Path


def load(d: Path | list[Path], name: str | None = None) -> tuple[str, list[dict]]:
    dirs = d if isinstance(d, list) else [d]
    rows = []
    for x in dirs:
        rows += [json.loads(l) for l in (x / "runs.jsonl").read_text().splitlines() if l.strip()]
    # A routed run served by more than one backend measures no single model:
    # never pooled into a model's column (see internal/route).
    mixed = sum(len(r.get("served_by") or {}) > 1 for r in rows)
    rows = [r for r in rows if len(r.get("served_by") or {}) <= 1]
    if mixed:
        print(f"<!-- {mixed} mixed-backend runs left out -->")
    rows = [r for r in rows if not r.get("invalid")]
    scored_tasks = collections.Counter(r["task"] for r in rows if not is_infra(r))
    dup = [t for t, n in scored_tasks.items() if n > 1]
    if dup:
        raise SystemExit(f"{[str(x) for x in dirs]}: tasks with more than one scored row: {dup}")
    rows = [r for r in rows if not (is_infra(r) and scored_tasks[r["task"]])]
    harness = {r.get("harness", "?") for r in rows if r.get("verdict") != "ERROR" or r.get("harness")}
    if len(harness) > 1:
        raise SystemExit(f"{d}: runs from several harness versions {sorted(harness)}; not pooling them")
    model = next((r.get("model") for r in rows if r.get("model")), dirs[0].name)
    label = name or f"{model} (h{next(iter(harness), '?')})"
    return label, rows


def is_infra(r: dict) -> bool:
    return bool(r.get("infra_error")) or r.get("verdict") == "ERROR"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("dirs", nargs="*", type=Path)
    ap.add_argument("--col", action="append", default=[], help='"Label=dir1,dir2,..." merged into one column')
    ap.add_argument("--split", type=Path)
    ap.add_argument("--difficulty-from", type=Path, help="directory of prepared tasks (task.json has swebench.difficulty)")
    ap.add_argument("--difficulty-json", type=Path, help="instance_id -> SWE-bench difficulty (results/swe/difficulty.json)")
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

    if args.difficulty_json:
        diff.update(json.loads(args.difficulty_json.read_text()))
    cols = [load(d) for d in args.dirs]
    for spec in args.col:
        label, _, ds = spec.partition("=")
        cols.append(load([Path(x) for x in ds.split(",") if x], label))
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

    out += ["", "`fail*` = the agent claimed it had fixed the task and the hidden tests disagreed (over-claim).",
            "`infra` = the run was stopped by infrastructure (or by an infra check) and is not scored, even if its",
            "final patch verified; those are counted in the 'of which PASS' column, never in the pass rate.", ""]
    for subset in (["held-out"], ["dev"], ["dev", "held-out"]) if split else ([None],):
        name = "+".join(subset) if subset[0] else "all"
        out.append(f"**{name}**")
        out.append("")
        out.append("| Model | Verified PASS / scored | by difficulty | Claimed | Over-claims | Infra (unscored) | of which PASS | Median tokens/run | Median min/run |")
        out.append("|---|---|---|---|---|---|---|---|---|")
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
            infra_pass = sum(is_infra(r) and r.get("verdict") == "PASS" for r in rs)
            out.append(f"| {label} | {p}/{len(scored)}{pct} | {bd} | {c} | {o} | {len(rs) - len(scored)} | {infra_pass} | {med(tok):,} | {med(mins):.1f} |")
        out.append("")
    print("\n".join(out))


if __name__ == "__main__":
    main()
