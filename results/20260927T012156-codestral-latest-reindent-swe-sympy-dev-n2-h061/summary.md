# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| sympy__sympy-13798 | 0/1 | 0/1 | 0 | 0 | 32 | 1 | 1 |
| sympy__sympy-17318 | 0/1 | 0/1 | 0 | 0 | 26 | 0 | 1 |
| sympy__sympy-18211 | 0/1 | 1/1 | 1 | 0 | 16 | 1 | 0 |
| sympy__sympy-13031 | 0/1 | 0/1 | 0 | 0 | 10 | 0 | 0 |
| sympy__sympy-14711 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| sympy__sympy-13877 | 1/1 | 1/1 | 0 | 0 | 9 | 1 | 0 |
| sympy__sympy-15875 | 0/1 | 0/1 | 0 | 0 | 19 | 0 | 0 |
| sympy__sympy-16792 | 0/1 | 0/1 | 0 | 0 | 24 | 4 | 1 |
| sympy__sympy-24443 | 0/1 | 0/1 | 0 | 0 | 27 | 4 | 1 |
| sympy__sympy-19346 | 1/1 | 0/1 | 0 | 0 | 31 | 4 | 1 |

**Total: 2/10 verified PASS over scored runs; agent claimed 2; over-claims 1; infra errors 0 (not scored); tool refusals 6.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
