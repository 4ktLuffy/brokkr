# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| sympy__sympy-19637 | 1/1 | 0/1 | 0 | 0 | 26 | 8 | 1 |
| sympy__sympy-11618 | 0/1 | 1/1 | 1 | 0 | 20 | 1 | 0 |
| sympy__sympy-23824 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| sympy__sympy-22914 | 1/1 | 1/1 | 0 | 0 | 7 | 1 | 0 |
| sympy__sympy-13480 | 0/1 | 1/1 | 1 | 0 | 8 | 1 | 0 |
| sympy__sympy-14531 | 0/1 | 0/1 | 0 | 0 | 25 | 0 | 1 |

**Total: 2/6 verified PASS over scored runs; agent claimed 3; over-claims 2; infra errors 0 (not scored); tool refusals 2.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
