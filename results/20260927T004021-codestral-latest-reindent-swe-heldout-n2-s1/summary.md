# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-16136 | 0/1 | 0/1 | 0 | 0 | 29 | 8 | 1 |
| django__django-14999 | 0/1 | 0/1 | 0 | 0 | 21 | 2 | 1 |
| django__django-16116 | 0/1 | 0/1 | 0 | 0 | 26 | 9 | 1 |
| django__django-15375 | 0/1 | 0/1 | 0 | 0 | 17 | 0 | 1 |
| django__django-16315 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-16256 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15252 | 0/1 | 1/1 | 1 | 0 | 7 | 2 | 0 |
| django__django-15561 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |
| django__django-15104 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-14765 | 0/1 | 1/1 | 1 | 0 | 5 | 1 | 0 |
| django__django-16139 | 1/1 | 1/1 | 0 | 0 | 5 | 1 | 0 |
| django__django-16255 | 1/1 | 1/1 | 0 | 0 | 5 | 1 | 0 |
| django__django-15268 | 0/1 | 1/1 | 1 | 0 | 8 | 2 | 0 |
| django__django-15987 | 1/1 | 1/1 | 0 | 0 | 6 | 1 | 0 |
| django__django-15022 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| django__django-15499 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |
| django__django-16429 | 1/1 | 1/1 | 0 | 0 | 7 | 1 | 0 |
| django__django-16333 | 1/1 | 1/1 | 0 | 0 | 8 | 1 | 0 |

**Total: 5/16 verified PASS over scored runs; agent claimed 8; over-claims 3; infra errors 2 (not scored); tool refusals 5.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
