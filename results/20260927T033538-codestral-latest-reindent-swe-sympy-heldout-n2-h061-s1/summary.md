# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| sympy__sympy-12096 | 0/1 | 0/1 | 0 | 0 | 27 | 0 | 1 |
| sympy__sympy-20428 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| sympy__sympy-16886 | 1/1 | 1/1 | 0 | 0 | 14 | 1 | 0 |
| sympy__sympy-16450 | 0/1 | 0/1 | 0 | 0 | 15 | 1 | 0 |
| sympy__sympy-19954 | 0/1 | 0/1 | 0 | 0 | 25 | 0 | 1 |
| sympy__sympy-19783 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| sympy__sympy-20801 | 0/1 | 0/1 | 0 | 0 | 22 | 4 | 1 |
| sympy__sympy-19495 | 0/1 | 1/1 | 1 | 0 | 10 | 1 | 0 |
| sympy__sympy-24562 | 0/1 | 1/1 | 1 | 0 | 10 | 1 | 0 |
| sympy__sympy-13372 | 0/1 | 0/1 | 0 | 0 | 32 | 0 | 1 |
| sympy__sympy-24539 | 1/1 | 1/1 | 0 | 0 | 6 | 1 | 0 |
| sympy__sympy-17630 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| sympy__sympy-12481 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| sympy__sympy-12419 | 0/1 | 0/1 | 0 | 0 | 24 | 3 | 1 |
| sympy__sympy-14976 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 2/14 verified PASS over scored runs; agent claimed 5; over-claims 3; infra errors 1 (not scored); tool refusals 6.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
