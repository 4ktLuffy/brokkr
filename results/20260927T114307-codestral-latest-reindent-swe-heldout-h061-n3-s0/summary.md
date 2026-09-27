# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-142-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-15572 | 0/1 | 0/1 | 0 | 0 | 23 | 0 | 1 |
| django__django-15554 | 0/1 | 0/1 | 0 | 0 | 13 | 0 | 1 |
| django__django-14155 | 0/1 | 1/1 | 1 | 0 | 14 | 2 | 0 |
| django__django-13925 | 0/1 | 0/1 | 0 | 0 | 6 | 0 | 0 |
| django__django-15851 | 0/1 | 0/1 | 0 | 0 | 40 | 9 | 0 |
| django__django-14007 | 0/1 | 0/1 | 0 | 0 | 18 | 0 | 1 |

**Total: 0/6 verified PASS over scored runs; agent claimed 1; over-claims 1; infra errors 0 (not scored); tool refusals 3.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
