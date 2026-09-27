# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| sympy__sympy-12489 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| sympy__sympy-21847 | 0/1 | 0/1 | 0 | 0 | 25 | 0 | 1 |
| sympy__sympy-18698 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| sympy__sympy-20590 | 0/1 | 0/1 | 0 | 0 | 16 | 1 | 0 |
| sympy__sympy-20916 | 0/1 | 1/1 | 1 | 0 | 13 | 1 | 0 |
| sympy__sympy-15345 | 0/1 | 0/1 | 0 | 0 | 29 | 0 | 1 |
| sympy__sympy-23413 | 0/1 | 0/1 | 0 | 0 | 15 | 0 | 1 |
| sympy__sympy-15976 | 0/1 | 0/1 | 0 | 0 | 35 | 0 | 1 |
| sympy__sympy-15017 | 0/1 | 0/1 | 0 | 0 | 23 | 4 | 1 |
| sympy__sympy-20154 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |
| sympy__sympy-16766 | 1/1 | 1/1 | 0 | 0 | 6 | 1 | 0 |
| sympy__sympy-24661 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| sympy__sympy-22714 | 1/1 | 1/1 | 0 | 0 | 7 | 1 | 0 |
| sympy__sympy-18763 | 1/1 | 0/1 | 0 | 0 | 15 | 2 | 1 |
| sympy__sympy-21596 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 3/13 verified PASS over scored runs; agent claimed 3; over-claims 1; infra errors 2 (not scored); tool refusals 7.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
