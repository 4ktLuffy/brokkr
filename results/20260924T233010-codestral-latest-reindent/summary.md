# Agent eval: codestral-latest, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 3. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| calc-mean-off-by-one | 3/3 | 3/3 | 0 | 0 | 5 | 1 | 0 |
| inventory | 2/3 | 2/3 | 0 | 0 | 7 | 1 | 1 |
| lru | 3/3 | 3/3 | 0 | 0 | 6 | 1 | 0 |
| slugify | 2/3 | 2/3 | 0 | 0 | 7 | 1 | 0 |
| workdays | 3/3 | 3/3 | 0 | 0 | 8 | 2 | 0 |

**Total: 13/15 verified PASS over scored runs; agent claimed 13; over-claims 0; infra errors 0 (not scored); tool refusals 1.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
