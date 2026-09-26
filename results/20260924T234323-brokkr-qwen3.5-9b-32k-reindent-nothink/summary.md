# Agent eval: brokkr-qwen3.5-9b-32k, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 3. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| calc-mean-off-by-one | 3/3 | 3/3 | 0 | 0 | 4 | 1 | 0 |
| inventory | 3/3 | 3/3 | 0 | 0 | 7 | 1 | 0 |
| lru | 3/3 | 3/3 | 0 | 0 | 7 | 1 | 0 |
| slugify | 1/2 | 2/2 | 1 | 1 | 18 | 5 | 2 |
| workdays | 3/3 | 3/3 | 0 | 0 | 5 | 1 | 1 |

**Total: 13/14 verified PASS over scored runs; agent claimed 14; over-claims 1; infra errors 1 (not scored); tool refusals 3.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
