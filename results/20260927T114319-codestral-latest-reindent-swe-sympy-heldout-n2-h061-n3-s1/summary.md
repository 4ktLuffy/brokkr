# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| sympy__sympy-22080 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| sympy__sympy-15349 | 0/1 | 0/1 | 0 | 0 | 36 | 0 | 1 |
| sympy__sympy-13647 | 0/1 | 0/1 | 0 | 0 | 38 | 0 | 1 |
| sympy__sympy-15599 | 0/1 | 1/1 | 1 | 0 | 10 | 1 | 1 |
| sympy__sympy-23950 | 0/1 | 0/1 | 0 | 0 | 23 | 8 | 1 |
| sympy__sympy-20438 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |

**Total: 0/6 verified PASS over scored runs; agent claimed 1; over-claims 1; infra errors 0 (not scored); tool refusals 4.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
