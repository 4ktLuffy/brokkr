#!/usr/bin/env python3
"""Fill the README's results block from results/ so no number is typed by hand.

    python3 scripts/fill-readme-results.py [--final]

Replaces the placeholders between <!-- RESULTS:START --> and <!-- RESULTS:END -->
in README.md, using the same directory globs as scripts/make-report.sh and the
same scoring (infra rows unscored) and paired test as scripts/compare.py.
Keeps a template in results/README-results.template.md on first use.
"""
from __future__ import annotations

import datetime
import glob
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from compare import mcnemar_exact, rows  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
README = ROOT / "README.md"
TEMPLATE = ROOT / "results" / "README-results.template.md"


def dirs(*patterns: str) -> str:
    out = []
    for p in patterns:
        out += sorted(glob.glob(str(ROOT / p)))
    return ",".join(out)


def score(label: str, spec: str) -> tuple[dict[str, bool], str]:
    if not spec:
        return {}, "not run"
    _, r = rows(f"{label}={spec}")
    p, n = sum(r.values()), len(r)
    return r, f"**{p} / {n} ({100 * p / n:.0f}%)**" if n else "no scored task yet"


PVALUES: list[float] = []


def paired(a: dict[str, bool], b: dict[str, bool], la: str, lb: str) -> str:
    common = sorted(set(a) & set(b))
    if not common:
        return "no common tasks yet"
    oa = sum(a[t] and not b[t] for t in common)
    ob = sum(b[t] and not a[t] for t in common)
    pa, pb = sum(a[t] for t in common), sum(b[t] for t in common)
    p = mcnemar_exact(oa, ob)
    PVALUES.append(p)
    return f"{lb} {pb}/{len(common)} vs {la} {pa}/{len(common)} on {len(common)} shared tasks ({ob}–{oa} discordant, p = {p:.3f})"


def stat_note(final: bool) -> str:
    """What the p-values do and do not show. Two comparisons are made, so the
    Bonferroni threshold is 0.05 / 2; while runs are unfinished each look is
    an interim look, and repeated looks inflate false positives."""
    alpha = 0.05 / max(1, len(PVALUES))
    passing = [p for p in PVALUES if p < alpha]
    looks = (" The qwen3.5 comparison covers only its first 18 tasks (paused) and was looked at repeatedly, so treat it as provisional."
             if final else " These are interim looks at unfinished runs; repeated looks inflate false positives, so treat them as provisional until the runs finish.")
    if not PVALUES:
        return "No paired comparison yet."
    if passing:
        return f"{len(passing)} of {len(PVALUES)} comparisons are below the Bonferroni threshold (α = {alpha:.3f} for {len(PVALUES)} comparisons).{looks}"
    return f"Neither comparison is below the Bonferroni threshold (α = {alpha:.3f} for {len(PVALUES)} comparisons), though the direction is consistent.{looks}"


def main() -> None:
    text = README.read_text()
    start, end = "<!-- RESULTS:START -->", "<!-- RESULTS:END -->"
    i, j = text.index(start), text.index(end)
    if not TEMPLATE.exists():
        TEMPLATE.write_text(text[i:j + len(end)])
    block = TEMPLATE.read_text()

    base, _ = score("b", dirs("results/*codestral-latest-reindent-swe-heldout-s[0-9]",
                              "results/*codestral-latest-reindent-swe-heldout-n2-s[0-9]"))
    new, new_s = score("n", dirs("results/*codestral-latest-reindent-swe-heldout-h061*"))
    qw, qw_s = score("q", dirs("results/*qwen3.5-9b-32k-reindent-swe-heldout-nothink",
                               "results/*qwen3.5-9b-32k-reindent-swe-heldout-n2-qwen"))
    _, sy_s = score("s", dirs("results/*codestral-latest-reindent-swe-sympy-heldout-n2-h061*"))

    when = datetime.datetime.now().strftime("%Y-%m-%d %H:%M local")
    for k, v in {
        "RESULTS_TIME": when,
        "CODESTRAL_061": new_s,
        "QWEN_031": qw_s,
        "SYMPY_061": sy_s,
        "PAIR_061": paired(base, new, "0.3.1", "0.6.1"),
        "PAIR_QWEN": paired(base, qw, "Codestral", "qwen3.5-9B"),
    }.items():
        block = block.replace(k, v)
    block = block.replace("STAT_NOTE", stat_note("--final" in sys.argv))
    if "--final" in sys.argv:
        block = block.replace("(in progress)", "").replace(", in progress", "")
    README.write_text(text[:i] + block + text[j + len(end):])
    print(block)


if __name__ == "__main__":
    main()
