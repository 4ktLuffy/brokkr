<!-- RESULTS:START -->
Snapshot taken RESULTS_TIME from `results/REPORT.md` (regenerate with
`scripts/make-report.sh`). The counts below are
final for Codestral; qwen3.5 is paused. Every verdict is Brokkr's own check against SWE-bench's hidden
tests. Held-out tasks were never used to shape the harness (`results/swe/plan.json`).

**Django 4.x, held-out (82 valid tasks):**

| Run | Verified | Notes |
|---|---|---|
| Codestral, harness 0.3.1 (pre-registered baseline) | **11 / 79 (14%)** | complete. 29% of "<15 min" tasks, 1 of 36 "15 min–1 h", 0 of 8 harder. **Claimed a fix 40 times; 30 were wrong** |
| Codestral, harness 0.6.1 | CODESTRAL_061 | complete |
| qwen3.5-9B, local on the Mac, harness 0.3.1 | QWEN_031 | paused after the first 18 tasks in pre-registered order |

**Paired, on the same tasks (exact McNemar test):**
- Codestral 0.6.1 vs 0.3.1: PAIR_061
- qwen3.5-9B vs Codestral: PAIR_QWEN

STAT_NOTE

**SymPy, held-out (65 valid tasks):** Codestral, harness 0.6.1: SYMPY_061, complete.

**pydantic, held-out (26 tasks, every fix merged after both models were released,
so they cannot have seen it):** Codestral, harness 0.7.0: PYD_CODESTRAL.

Across Django and SymPy, 167 of the 169 prepared tasks passed the validity check:
Django 92/94 and SymPy 75/75. The Brokkr ports of SWE-bench's log parsers agree with
the originals on every validation log (338 logs).
<!-- RESULTS:END -->