# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-14539 | 0/1 | 0/1 | 0 | 0 | 38 | 0 | 1 |
| django__django-15277 | 1/1 | 0/1 | 0 | 0 | 40 | 0 | 1 |
| django__django-14999 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-14672 | 1/1 | 1/1 | 0 | 0 | 10 | 1 | 0 |
| django__django-14725 | 0/1 | 0/1 | 0 | 0 | 25 | 3 | 1 |
| django__django-14608 | 0/1 | 0/1 | 0 | 0 | 40 | 1 | 0 |
| django__django-15973 | 0/1 | 0/1 | 0 | 0 | 36 | 0 | 0 |
| django__django-14631 | 0/1 | 0/1 | 0 | 0 | 28 | 0 | 1 |
| django__django-16263 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15503 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| django__django-15561 | 0/1 | 0/1 | 0 | 0 | 10 | 0 | 0 |
| django__django-14011 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |
| django__django-13964 | 0/1 | 0/1 | 0 | 0 | 15 | 0 | 0 |
| django__django-14500 | 1/1 | 1/1 | 0 | 0 | 17 | 1 | 1 |
| django__django-15382 | 0/1 | 0/1 | 0 | 0 | 18 | 0 | 1 |
| django__django-14351 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 3/14 verified PASS over scored runs; agent claimed 2; over-claims 0; infra errors 2 (not scored); tool refusals 7.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
