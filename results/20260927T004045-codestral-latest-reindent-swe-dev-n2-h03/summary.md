# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 1. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| django__django-15957 | 0/1 | 0/1 | 0 | 0 | 20 | 4 | 1 |
| django__django-16100 | 0/1 | 0/1 | 0 | 0 | 28 | 10 | 1 |

**Total: 0/2 verified PASS over scored runs; agent claimed 0; over-claims 0; infra errors 0 (not scored); tool refusals 2.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
