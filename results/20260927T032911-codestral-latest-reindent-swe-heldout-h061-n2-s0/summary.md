# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-14122 | 0/1 | 0/1 | 0 | 0 | 15 | 0 | 1 |
| django__django-16136 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 0 |
| django__django-15128 | 0/1 | 0/1 | 0 | 0 | 15 | 0 | 1 |
| django__django-14915 | 1/1 | 1/1 | 0 | 0 | 7 | 1 | 0 |
| django__django-15814 | 0/1 | 0/1 | 0 | 0 | 14 | 0 | 1 |
| django__django-14752 | 1/1 | 0/1 | 0 | 0 | 15 | 1 | 1 |
| django__django-16315 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-16256 | 0/1 | 0/1 | 0 | 0 | 15 | 0 | 1 |
| django__django-14017 | 0/1 | 0/1 | 0 | 0 | 24 | 0 | 1 |
| django__django-15252 | 0/1 | 0/1 | 0 | 0 | 25 | 0 | 1 |
| django__django-15732 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15741 | 1/1 | 0/1 | 0 | 0 | 23 | 0 | 1 |
| django__django-15104 | 1/1 | 1/1 | 0 | 0 | 11 | 1 | 0 |
| django__django-14238 | 0/1 | 0/1 | 0 | 0 | 14 | 0 | 1 |
| django__django-14765 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |

**Total: 4/14 verified PASS over scored runs; agent claimed 2; over-claims 0; infra errors 1 (not scored); tool refusals 9.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
