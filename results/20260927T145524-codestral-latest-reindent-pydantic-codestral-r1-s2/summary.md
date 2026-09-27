# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| pydantic__pydantic-12809 | 1/1 | 1/1 | 0 | 0 | 8 | 1 | 0 |
| pydantic__pydantic-13123 | 0/1 | 0/1 | 0 | 0 | 36 | 0 | 1 |
| pydantic__pydantic-13419 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 1/2 verified PASS over scored runs; agent claimed 1; over-claims 0; infra errors 1 (not scored); tool refusals 1.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
