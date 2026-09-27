# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-15277 | 0/1 | 1/1 | 1 | 0 | 5 | 1 | 0 |
| django__django-14915 | 1/1 | 1/1 | 0 | 0 | 5 | 1 | 0 |
| django__django-15814 | 0/1 | 1/1 | 1 | 0 | 5 | 1 | 0 |
| django__django-14752 | 1/1 | 1/1 | 0 | 0 | 5 | 1 | 0 |
| django__django-15973 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-14631 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15503 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15741 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| django__django-15916 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| django__django-15382 | 0/0 | 0/0 | 0 | 1 | - | - | 0 |
| django__django-15278 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-16082 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15380 | 0/1 | 1/1 | 1 | 0 | 6 | 1 | 0 |
| django__django-15572 | 0/1 | 0/1 | 0 | 0 | 40 | 0 | 0 |
| django__django-15554 | 0/1 | 1/1 | 1 | 0 | 12 | 1 | 0 |
| django__django-16145 | 0/1 | 1/1 | 1 | 0 | 9 | 1 | 0 |
| django__django-15851 | 0/1 | 0/1 | 0 | 0 | 15 | 4 | 0 |
| django__django-15368 | 1/1 | 1/1 | 0 | 0 | 5 | 1 | 0 |

**Total: 3/17 verified PASS over scored runs; agent claimed 10; over-claims 7; infra errors 1 (not scored); tool refusals 0.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
