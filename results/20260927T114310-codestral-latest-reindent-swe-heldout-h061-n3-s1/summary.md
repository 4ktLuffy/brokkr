# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-15467 | 1/1 | 0/1 | 0 | 0 | 17 | 0 | 1 |
| django__django-15499 | 0/1 | 0/1 | 0 | 0 | 29 | 0 | 0 |
| django__django-13933 | 0/1 | 0/1 | 0 | 0 | 14 | 0 | 1 |
| django__django-15525 | 0/1 | 1/1 | 1 | 0 | 14 | 0 | 0 |
| django__django-14792 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| django__django-15368 | 1/1 | 1/1 | 0 | 0 | 10 | 1 | 0 |

**Total: 2/6 verified PASS over scored runs; agent claimed 3; over-claims 2; infra errors 0 (not scored); tool refusals 2.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
