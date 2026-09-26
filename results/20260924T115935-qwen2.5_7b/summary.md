# Agent eval: qwen2.5:7b

Host: Apple M4 16GiB / Lima vz nested virt / 6.8.0-139-generic  
Runs per task: 3. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.

| Task | Verified PASS | Agent claimed fixed | Over-claims | Median turns | Median test runs | Refusals |
|---|---|---|---|---|---|---|
| calc-mean-off-by-one | 3/3 | 3/3 | 0 | 4 | 1 | 0 |
| inventory | 0/3 | 0/3 | 0 | 20 | 1 | 0 |
| lru | 0/3 | 0/3 | 0 | 20 | 2 | 1 |
| slugify | 1/3 | 2/3 | 1 | 16 | 3 | 3 |
| workdays | 0/3 | 0/3 | 0 | 12 | 5 | 1 |

**Total: 4/15 verified PASS; agent claimed 5; over-claims 1; sandbox/model errors 0; tool refusals 5.**

An over-claim is a run where the agent said it fixed the task and verification disagreed.
