# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-16100 | 0/1 | 0/1 | 0 | 0 | 22 | 0 | 1 |
| django__django-13809 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| django__django-15315 | 0/1 | 0/1 | 0 | 0 | 24 | 5 | 0 |
| django__django-15731 | 1/1 | 0/1 | 0 | 0 | 22 | 1 | 0 |
| django__django-14787 | 0/1 | 0/1 | 0 | 0 | 26 | 0 | 1 |
| django__django-15695 | 0/1 | 0/1 | 0 | 0 | 40 | 4 | 10 |
| django__django-15563 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15957 | 0/1 | 0/1 | 0 | 0 | 23 | 5 | 1 |
| django__django-15280 | 0/1 | 0/1 | 0 | 0 | 30 | 1 | 0 |
| django__django-14373 | 1/1 | 1/1 | 0 | 0 | 11 | 1 | 0 |

**Total: 2/10 verified PASS over scored runs; agent claimed 2; over-claims 1; infra errors 0 (not scored); tool refusals 13.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
