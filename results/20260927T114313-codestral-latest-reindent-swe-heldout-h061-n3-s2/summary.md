# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-15022 | 0/1 | 0/1 | 0 | 0 | 18 | 0 | 0 |
| django__django-16145 | 1/1 | 0/1 | 0 | 0 | 38 | 0 | 1 |
| django__django-14170 | 0/1 | 0/1 | 0 | 0 | 21 | 0 | 1 |
| django__django-16429 | 1/1 | 0/1 | 0 | 0 | 19 | 3 | 1 |
| django__django-16333 | 1/1 | 0/1 | 0 | 0 | 21 | 1 | 0 |
| django__django-14771 | 0/1 | 0/1 | 0 | 0 | 11 | 0 | 0 |

**Total: 3/6 verified PASS over scored runs; agent claimed 0; over-claims 0; infra errors 0 (not scored); tool refusals 3.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
