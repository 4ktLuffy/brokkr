# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-14539 | 0/1 | 0/1 | 0 | 0 | 40 | 1 | 0 |
| django__django-15128 | 0/1 | 0/1 | 0 | 0 | 14 | 0 | 1 |
| django__django-14672 | 0/1 | 1/1 | 1 | 0 | 5 | 1 | 0 |
| django__django-14725 | 0/1 | 1/1 | 1 | 0 | 7 | 1 | 0 |
| django__django-14855 | 1/1 | 1/1 | 0 | 0 | 7 | 1 | 0 |
| django__django-15930 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-16263 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15732 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15629 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15161 | 0/1 | 0/1 | 0 | 0 | 28 | 10 | 1 |
| django__django-15127 | 0/1 | 1/1 | 1 | 0 | 5 | 1 | 0 |
| django__django-15098 | 0/1 | 1/1 | 1 | 0 | 16 | 2 | 0 |
| django__django-15569 | 1/1 | 0/1 | 0 | 0 | 17 | 9 | 2 |
| django__django-15863 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15467 | 0/1 | 0/1 | 0 | 0 | 17 | 0 | 1 |
| django__django-16032 | 0/1 | 0/1 | 0 | 0 | 19 | 0 | 1 |
| django__django-15525 | 0/1 | 1/1 | 1 | 0 | 15 | 2 | 0 |
| django__django-14792 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-14771 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |

**Total: 2/19 verified PASS over scored runs; agent claimed 6; over-claims 5; infra errors 0 (not scored); tool refusals 6.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
