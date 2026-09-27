# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| sympy__sympy-21930 | 0/1 | 0/1 | 0 | 0 | 16 | 0 | 0 |
| sympy__sympy-13974 | 0/1 | 1/1 | 1 | 0 | 34 | 3 | 3 |
| sympy__sympy-18199 | 0/1 | 1/1 | 1 | 0 | 24 | 9 | 1 |
| sympy__sympy-13878 | 0/1 | 0/1 | 0 | 0 | 33 | 0 | 1 |
| sympy__sympy-17655 | 0/1 | 0/1 | 0 | 0 | 25 | 0 | 1 |
| sympy__sympy-13551 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 12 |

**Total: 0/6 verified PASS over scored runs; agent claimed 2; over-claims 2; infra errors 0 (not scored); tool refusals 18.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
