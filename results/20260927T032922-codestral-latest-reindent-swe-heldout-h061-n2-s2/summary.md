# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-14580 | 0/1 | 0/1 | 0 | 0 | 14 | 0 | 1 |
| django__django-14140 | 0/1 | 0/1 | 0 | 0 | 25 | 3 | 0 |
| django__django-14434 | 0/1 | 0/1 | 0 | 0 | 20 | 0 | 1 |
| django__django-16116 | 0/1 | 0/1 | 0 | 0 | 28 | 9 | 2 |
| django__django-15375 | 0/1 | 1/1 | 1 | 0 | 25 | 0 | 3 |
| django__django-14855 | 1/1 | 0/1 | 0 | 0 | 15 | 1 | 1 |
| django__django-15930 | 0/1 | 0/1 | 0 | 0 | 27 | 0 | 1 |
| django__django-14034 | 0/1 | 0/1 | 0 | 0 | 14 | 0 | 1 |
| django__django-14493 | 1/1 | 1/1 | 0 | 0 | 11 | 1 | 0 |
| django__django-14559 | 0/1 | 1/1 | 1 | 0 | 24 | 0 | 3 |
| django__django-14404 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| django__django-15629 | 0/1 | 0/1 | 0 | 0 | 9 | 0 | 0 |
| django__django-15916 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 2/12 verified PASS over scored runs; agent claimed 4; over-claims 3; infra errors 1 (not scored); tool refusals 13.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
