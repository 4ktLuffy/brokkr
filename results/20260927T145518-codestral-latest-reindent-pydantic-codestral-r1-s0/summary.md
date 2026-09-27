# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| pydantic__pydantic-13692 | 1/1 | 1/1 | 0 | 0 | 9 | 1 | 0 |
| pydantic__pydantic-13272 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| pydantic__pydantic-13215 | 1/1 | 1/1 | 0 | 0 | 11 | 1 | 0 |
| pydantic__pydantic-13051 | 1/1 | 1/1 | 0 | 0 | 9 | 1 | 0 |
| pydantic__pydantic-13453 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| pydantic__pydantic-13713 | 0/1 | 0/1 | 0 | 0 | 11 | 0 | 0 |
| pydantic__pydantic-13675 | 0/1 | 0/1 | 0 | 0 | 21 | 0 | 0 |
| pydantic__pydantic-12704 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 3/7 verified PASS over scored runs; agent claimed 3; over-claims 0; infra errors 1 (not scored); tool refusals 1.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
