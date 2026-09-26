# Agent eval: brokkr-qwen2.5-7b-16k, strict edits

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 3. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.
Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.

| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|---|
| calc-mean-off-by-one | 3/3 | 3/3 | 0 | 0 | 4 | 1 | 0 |
| inventory | 0/3 | 0/3 | 0 | 0 | 20 | 0 | 0 |
| lru | 0/3 | 0/3 | 0 | 0 | 20 | 1 | 0 |
| slugify | 0/3 | 2/3 | 2 | 0 | 10 | 5 | 4 |
| workdays | 0/3 | 0/3 | 0 | 0 | 20 | 4 | 0 |

**Total: 3/15 verified PASS over scored runs; agent claimed 5; over-claims 2; infra errors 0 (not scored); tool refusals 4.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
