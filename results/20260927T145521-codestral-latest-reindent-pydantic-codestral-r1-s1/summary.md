# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| pydantic__pydantic-13017 | 0/1 | 1/1 | 1 | 0 | 23 | 1 | 0 |
| pydantic__pydantic-13771 | 1/1 | 1/1 | 0 | 0 | 33 | 1 | 0 |
| pydantic__pydantic-11134 | 0/1 | 0/1 | 0 | 0 | 17 | 1 | 0 |
| pydantic__pydantic-11043 | 0/1 | 0/1 | 0 | 0 | 15 | 1 | 0 |
| pydantic__pydantic-13687 | 1/1 | 1/1 | 0 | 0 | 12 | 2 | 0 |
| pydantic__pydantic-7987 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 2/5 verified PASS over scored runs; agent claimed 3; over-claims 1; infra errors 1 (not scored); tool refusals 0.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
