# Why agents fail: a weakness map

Generated 2026-09-27 15:37 by `scripts/analyze_failures.py` over every scored run on
real tasks (Django, SymPy and pydantic; dev and held-out; Codestral and qwen3.5). Each
failed run is classified from its own final patch, the task's gold patch and the final
test evidence. The first bucket that fits wins.

**321 scored runs: 65 pass, 256 fail.**

| Why it failed | Runs | Share of failures |
|---|---|---|
| never edited (no change) | 110 | 43% |
| incomplete fix: right file, target tests still fail | 56 | 22% |
| wrong location: none of the files the real fix edits | 31 | 12% |
| broke existing tests | 26 | 10% |
| out of turns (with an edit) | 17 | 7% |
| broke syntax | 16 | 6% |

**Never edited, split by why the agent stopped** (hand-checked samples):

| Stop reason | Runs | What the agent spent its calls on |
|---|---|---|
| submitted with nothing | 48 | run_python 43%, read 18%, search 18%. It reproduced repeatedly, then gave up |
| out of turns | 47 | read 45%, search 37%. It explored until the budget ran out |
| stuck (loop detection) | 15 | the same search or read repeated |

**Other findings:**
- A reproduction helps: 24% pass when the agent ran one, 10% when it did not.
- Multi-file fixes are almost never solved: 1/39 (3%) when the real fix spans more
  than one file, against 64/282 (23%) for single-file fixes.
- Over-claims: 61 of 109 fix claims were wrong (incomplete fix 37, wrong location 16,
  broke existing 5, no change 3).

**What to fix first:** committing to an edit. 95 runs (37% of failures) never changed a
file, either reproducing repeatedly or reading until the turns ran out. Harness 0.8
targets that.
