# Agent eval: brokkr-qwen2.5-7b-16k, reindent edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 3. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| calc-mean-off-by-one | 3/3 | 3/3 | 0 | 0 | 4 | 1 | 0 |
| inventory | 1/3 | 1/3 | 0 | 0 | 20 | 2 | 0 |
| lru | 2/3 | 3/3 | 1 | 0 | 12 | 3 | 1 |
| slugify | 0/3 | 3/3 | 3 | 0 | 11 | 5 | 4 |
| workdays | 0/3 | 1/3 | 1 | 0 | 20 | 4 | 2 |

**Total: 6/15 verified PASS over scored runs; agent claimed 11; over-claims 5; infra errors 0 (not scored); tool refusals 7.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
