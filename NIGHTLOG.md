# Night log: 2026-09-24 → 25

What was done overnight, in order, with the numbers and the mistakes. Nothing here
is committed; everything is staged for review.

**Goal:** Brokkr fixing real open-source bugs (SWE-bench tasks) inside Firecracker,
verified, with results for a local model (`qwen3.5:9b`) and a free hosted model
(Mistral `codestral-latest`, free plan, through freetier).

**Ground rules kept:** no commits or pushes (diff first, then Henos decides); no
spending; each finding is checked against its source before it is written here.

## Log

### 23:30 Codestral on the five practice tasks: 13/15, 0 over-claims
- Harness 0.2, K=3, via `tools/freetier_proxy.py` → freetier → Mistral free plan.
- Per task: calc 3/3, inventory 2/3, lru 3/3, slugify 2/3, workdays 3/3.
- Both failures were honest (`fixed=false`), unlike qwen2.5:7b, which over-claimed 5
  of 11 times.
- About 240K tokens in total, logged in freetier's shared ledger.
- **Takeaway:** the practice tasks no longer separate models. Real tasks are needed.

### Mistral free plan, measured (not assumed)
- The key was checked for length and against the models list without being printed.
- From live response headers:
  - `codestral-latest`: **125 req/min, 625,000 tokens/min**.
  - `mistral-medium-latest` and `mistral-small-latest`: **0 req/min**, so they are not
    in the free plan.
  - Devstral is not offered to this account.
- No daily or monthly cap is published, so `dev/freetier/providers.yaml` sets a
  self-imposed 3M tokens/day and marks it as such.
- A zero-limit model is refused locally with no network call (tested).

### 23:45 qwen3.5:9b (local) on the practice tasks: 13/15 when scored strictly
- Chosen over qwen2.5-coder:14b from published numbers: BFCL-V4 66.1, τ²-bench 79.1,
  LiveCodeBench 65.6. Its KV cache is about 32 KB per token (8 of 32 layers use full
  attention), so 32K of context costs about 1 GB.
- The 14B coder would need about 12 GB at 16K context, which on a 16 GB Mac means
  swapping.
- A 32K-context Ollama tag was created (`dev/ollama/qwen3.5-9b-32k.Modelfile`). The
  default serves 4K and truncates, as the probe confirms.
- Thinking off (`reasoning_effort: none`), with Qwen's recommended instruct sampling
  (T 0.7, top_p 0.8, presence 1.5): 13 of 14 scored, plus 1 run that ended on a
  malformed tool call.
- **Correction:** that malformed-call run was first counted as an infra error. It is
  the model's own output, so strictly it is **13/15**. Harness 0.3 now retries a
  malformed reply twice and then scores it as a failure; only real infra problems
  are unscored.
- Compared with qwen2.5:7b's 6/15, and 13/15 for Codestral.

### Mistakes I made and caught tonight
- `scripts/probe-context.py` called qwen3.5 "TRUNCATED" when it wasn't: the model
  spent its 5 reply tokens thinking. The probe now disables thinking and judges by
  token count too.
- The first Django env silently ran without its virtualenv (`asgiref` missing). I
  had symlinked `/opt/env/bin` to `venv/bin`; Python finds `pyvenv.cfg` via the path
  as written and missed it. Fixed by making `/opt/env` the venv, with a check at
  build time that `/opt/env/bin/python3` really uses it. The validity check caught
  this: the gold patch FAILED.
- Two Go bugs in my own harness changes, both caught by tests or review: the
  truncation check was disabled permanently after 6 tool outputs, and the
  compaction counter was written after `summary.json`.
- **A leak I nearly shipped.** The agent's first message showed baseline test output,
  which for SWE-bench would reveal the hidden tests' names and assertions. Hidden-test
  tasks now show only the issue text, as in SWE-bench.

### 00:10 SWE-bench Verified in Firecracker
- **Source:** dataset sha256 `a45b1fe4…`; env specs from SWE-bench **v4.1.0** (pinned).
  Current main no longer carries per-repo specs in code.
- **Task set:** Django 4.0/4.1/4.2, 94 tasks. Pure Python, and Django's runner prints
  unittest-style output.
- **Environments:** three images at `/opt/env`, attached as a read-only third drive.
  Python 3.8.20 (4.0) and 3.9.25 (4.1, 4.2) from uv standalone builds, because
  conda is not available on arm64. 0 skipped requirements.
- **Differences from SWE-bench, recorded in each env's manifest:** uv instead of
  conda; Django not installed, with the working copy imported via PYTHONPATH.
- **Hidden tests:** the verifier applies SWE-bench's `test_patch` itself, after the
  candidate patch. The agent never sees it; `run_tests` runs visible tests only.
- **Parser:** a Go port of SWE-bench v4.1.0's `parse_log_django`, cross-checked
  against the original Python on every real log (`scripts/swe/crosscheck_parser.py`).
  Identical so far.
- **Split, fixed before any agent run** (`results/swe/split.json`, sha256
  `e723c989…`): 10 dev tasks, 84 held-out, chosen by sha256(instance_id).
- **Sandbox bug found by the validity check:** `django__django-13809`'s gold patch
  failed because the guest had no loopback, so tests that serve files on localhost
  could not connect. Loopback is now up. There is still no network device, and
  gate 1's `forge` case still gets "Network is unreachable" for 1.1.1.1. All tasks
  are re-validated on the new image.
- **First real fix:** Codestral solved `django__django-14373` (dev) in 2m19s,
  verified against the hidden tests (20/20 required pass).

### Harness 0.3 (agent)
- Tools for real repos: `list_dir`, `search` (regex, 50 hits), `read_file` with
  ranges (300 lines per call), and `run_python`, which runs code in a fresh microVM
  and never becomes part of the diff.
- A clearer system prompt: locate, understand, reproduce, fix, check, submit
  honestly.
- Older tool outputs are shortened in batches, only when the prompt passes 16K
  estimated tokens, so the prompt prefix stays stable for Ollama's cache.
- Budgets (turns, sandbox runs, compaction threshold) and sampling are recorded in
  every `summary.json`.

### 00:35 Throughput: VM resized
- Django test modules are CPU-bound. The VM had 4 vCPUs at load 4.0, while the
  M4 has 10 cores.
- The VM was restarted with **8 vCPUs and 5 GiB** (it was 4 and 8). Firecracker only
  commits memory the guest touches: three Django microVMs used 326 MB of RSS in
  total, so 5 GiB is ample and leaves room on the Mac for the local 9B model.
- Recorded in `dev/lima.yaml`.

### 00:39 A mistake in my eval change, caught before it cost anything
- I made `eval.sh` reuse `validate.sh`'s results (`VALIDITY=`) instead of re-running
  baseline and gold for every task, which saves about 45 s per task.
- That skipped the step that happened to create each task's run directory, so the
  shell could not open `run-1.stderr` and never started the agent: 10 of 10
  "infra errors" on both models.
- The summary correctly refused to score them. The directory is now created
  explicitly, both bogus result folders were deleted, and a check that agent
  processes actually start now comes before trusting a launch.

### 00:52 Held-out plan fixed before any held-out run (`results/swe/plan.json`)
- **Codestral:** all 84 held-out tasks, 3 parallel shards in the split's order.
- **qwen3.5:** the same order, one task at a time, as many as finish; reported as
  "the first N".
- Both use harness 0.3.1 (0.3.0 plus a corrected recorded field), with 40 turns, 10
  sandbox runs and compaction at 16K.
- Why qwen is limited: its first real task took about 11 minutes for 19 turns, so
  all 84 would take more than a day.

### 00:55 Dev findings: Codestral over-claims on real tasks
- On `django__django-13809` and `django__django-14787` Codestral submitted
  `fixed=true` after one visible test run. The hidden tests failed.
- In 14787 the issue contains a runnable example that the agent never ran. Its fix
  copied `__name__`/`__module__`; the gold fix wraps the partial with `wraps()`.
- Also found: my `claim_against_own_tests` field misread `VISIBLE_PASS` as a failure
  (fixed in 0.3.1; the field only).

### Harness 0.4.0 (dev experiment, separate binary)
- **Prompt:** reproduce before editing and confirm afterwards, and "passing the
  existing tests is not evidence of a fix; they did not catch this issue".
- **Gate:** on hidden-test tasks, a `fixed=true` submit with no `run_python` since the
  last edit gets one reminder, once per run.
- **Tests:** a scripted-model test, which fails with the gate disabled.
- **Decision rule, recorded in plan.json before any 0.4 run:** a held-out 0.4 run
  follows only if 0.4 verifies more dev tasks than 0.3 with Codestral, and is then
  reported as its own column.

### 01:10 Invalid tasks found by validation, with causes (so far 2)
- `django__django-15103`: the gold patch fails `test_strip_tags`. The test depends on
  `html.parser`, which changed in later Python security releases. Our env has
  3.8.20; SWE-bench's conda image has an older 3.8.
- `django__django-15037`: the gold patch passes every *required* test, but the run
  exits 1 because `test_custom_fields` (inspectdb; not in F2P/P2P) fails. Its SQLite
  type introspection differs: uv's standalone Python bundles a newer SQLite.
- **Brokkr's PASS is stricter than SWE-bench's.** It requires exit 0 as well as every
  required test; SWE-bench checks only the required tests. I kept the stricter rule
  rather than change the pass rule minutes before the held-out run. It can only
  under-count the agents, never over-count them. Both tasks are excluded as invalid,
  with these causes.

### 01:20 Dev findings, round 0.3 vs 0.4 (in progress)
- 0.4 Codestral PASSED `django__django-13809`, which 0.3 failed. It claimed
  `fixed=false`, an under-claim, after reproducing twice.
- qwen3.5 (0.3) PASSED 13809 after 26 turns and 5 `run_python` reproductions (22
  min). Its patch matches the gold approach, plus one unrelated deleted comment
  line.
- 0.3 Codestral, `django__django-15315`: **the same failing `replace_in_file` call 35
  times** ("old_text not found", with a hint) until the 40-turn budget ran out.
- 0.3 Codestral, `django__django-15280` (1–4 h): budget exhausted; honest
  `fixed=false`.
- **Planned 0.5, from this evidence:**
  - detect repeated identical failing calls: a blunt instruction after 3, end the run
    as "stuck" after about 6;
  - a `replace_lines(path, start, end, new_text)` tool, because exact-text matching is
    what models keep getting wrong.
  - It will be built after this dev round, so that round's numbers stay clean.

### 01:30 Harness 0.5.0 written and tested (not yet run)
- A `replace_lines` tool.
- A failing call repeated 3 times gets a blunt instruction; 6 times ends the run as
  "stuck", which is scored.
- Tests cover both, with a negative control (different failing calls are not a loop).
- Installed as a separate binary so nothing running is affected.

### 01:30 Token cap raised; two scheduling decisions (none based on held-out results)
- **Measured:** real SWE-bench runs cost 11K–359K tokens each (1.0M for the first 6
  Codestral dev runs). The self-imposed 3M/day cap was sized from practice-task runs
  and would stop the held-out run partway.
- **Raised to 15M/day** in `dev/freetier/providers.yaml`, with the reason. It is still
  self-imposed; if Mistral's real limit is lower, its 429 parks the run cleanly
  through freetier.
- **Second proxy:** a second freetier proxy on port 11501 carries the new cap for the
  held-out runs. Restarting the first would have broken in-flight dev runs.
- **qwen dev stops after 5 tasks** and qwen's held-out run starts. At about 15 min per
  task qwen cannot do all 10 dev tasks and a meaningful held-out slice; held-out
  results are what matter. The held-out order stays the one fixed in split.json.

### 01:45 Measured: loops cause most budget exhaustion (supports 0.5)
Codestral 0.3 dev runs that used all 40 turns, by longest run of identical calls:

| Task | Calls | Longest identical run | Tool errors |
|---|---|---|---|
| 15315 | 40 | 35 | 35 |
| 15695 | 40 | 25 | 32 |
| 15280 | 40 | 19 | 19 |
| 15563 | 41 | 1 | 0 (no loop; it did not find a fix) |

**3 of 4** budget-exhausted runs are loops on a failing call, each costing
184K–489K tokens.

### 02:00 The old 3M cap parked the dev runs; handled as designed
- Proxy 11500 (still 3M/day) parked Codestral at 2.99M tokens for UTC day 09-24.
  The two dev evals stopped, wrote `PARTIAL`, and their parked runs were listed as
  infra, not scored.
- Held-out runs use proxy 11501 (15M) and kept going. The park was not persisted to
  the shared ledger, so it did not spread.
- **Resumed on 11501:** 0.3's missing dev tasks (15957, 16100), 0.4's 7 missing, and a
  full 0.5 dev run. Proxy 11500 is stopped.
- **Validation finished:** 92/94 valid. The parser cross-check agreed on all 188
  logs. Both invalid tasks (15037, 15103) are held-out, so 82 held-out tasks are
  scored.
- Held-out 0.3.1 is already showing loops (`13964`: 40 turns in 138 s). That is the
  pre-registered baseline, left as is.

### 02:25 A second loop type; harness 0.6.0
- 0.5 FAILED dev task 14373, which 0.3 and 0.4 had solved. Codestral cycled
  search → read_file (the same 130 lines) → search ("no matches") about 10 times.
  None of those calls fail, so 0.5's failing-call rule never fired.
- **Correlation, not proven cause.** Over all Codestral runs, 7 of 9 compacted runs
  ran out of turns, against 4 of 20 uncompacted ones. Long runs are both compacted
  and more likely to run out, so compaction is not blamed and not changed.
- **0.6.0:** any identical successful call repeated with no edit in between gets a
  warning at 3 and ends the run as stuck at 5 (scored). Failing calls stay under
  0.5's rule.
- **Tests:** the 14373 cycle ends as stuck; the negative control (the same read
  around edits) passes untouched.
- **plan.json amended before any 0.4+ held-out run:** of 0.4, 0.5 and 0.6, the one
  with the most dev passes (ties: fewer tokens) gets **one** Codestral held-out run,
  and only if it beats 0.3 on dev. No further harness change is run on held-out
  tasks tonight.

### 02:20 Order bug found before it mattered; one risky edit
- **Order bug:** `eval.sh` ran TASKS alphabetically, using the list only as a filter.
  qwen's held-out run therefore started with 13925, not 14122 as plan.json says.
  Alphabetical order also means older Django versions first. Fixed so TASKS runs in
  the given order. qwen's held-out run was stopped after one unfinished task (no rows
  written) and restarted in plan order.
- **qwen dev had actually finished a 5th task:** 15315 **PASS** (the diff was right
  even though it used all 40 turns). **qwen3.5 dev: 3/5**, against Codestral 0.3's 1/9.
- **Risk I created:** I edited `eval.sh` in place while five evals were running it.
  Their loops were already parsed, so result rows are safe; the trailing summary step
  may misread shifted bytes. Summaries are regenerated with `scripts/report.py`.
  Rule from now on: write a new file and rename it; never edit a running script in
  place.
- **Same mistake, twice:** `pkill -f PATTERN` run through `bash -lc "..."` also kills
  that shell when its own command line contains PATTERN. Twice this cut a
  verification short, and the old qwen loop survived the first attempt and started
  more tasks (13925, 13933, 13964).
- The old run was then killed by PID (parent eval.sh first) and confirmed gone. Its
  rows went into a directory I had already deleted, so no bad row is in results/.
- qwen's held-out run is now running in plan order, starting with 14122.
- Rule: kill by PID, and verify with patterns that cannot match the verifying shell.
- **Correction, 02:52:** the old qwen loop was *still* running. PID 13298, which I
  had killed, was only a command-substitution subshell; the loop itself was an
  orphaned `eval.sh` (PID 13081), found by walking up from the running task's
  process. It is now killed with its whole tree.
- It could not record results (its results directory was gone), but for about 30
  minutes it shared the GPU with the real qwen run. The wall time of qwen's first
  held-out task (14122) is inflated; its verdict and turns are not affected.

### 03:05 A false positive in my truncation detector (fixed as 0.6.1, not run tonight)
- 0.6.0 stopped Codestral on dev task 15280 as "context truncated": Mistral's
  reported prompt went 13,326 → 12,771 while our estimate grew. At 13K of a 256K
  window nothing can be truncated; it is Mistral's own token accounting.
- The shrink rule never checked distance to the window. **0.6.1** applies it only
  when the (deliberately high) estimate is within 80% of the window. Tested both
  ways: the old truncation case still fires at ~83% of the window; the Mistral case
  does not.
- Per plan.json, no harness change is run on held-out tasks tonight, so tonight's
  runs keep 0.6.0 behaviour. Such stops are reported as infra and not scored, and
  are counted in the morning report.
- **Mistral usage:** 8.39M tokens on UTC 09-24 (under the 15M self-cap); the new UTC
  day began at 03:00 local.

### 03:18 Stopped by request
Everything was stopped at 03:18: all evals, the VM (20 agent and Firecracker
processes), the freetier proxy, and the local model. Runs in progress at that moment
wrote no row, or an unscored ERROR row. The result directories are left as they
were; evals that were cut off have no `summary.md`, so use `scripts/report.py`.

Where things stand:

| Run | Scored | PASS | Over-claims |
|---|---|---|---|
| Codestral 0.3.1, held-out | 27 | 1 | 15 |
| Codestral 0.3, dev | 8 | 1 | 2 |
| Codestral 0.4, dev (both parts) | 8 | 3 | 0 |
| Codestral 0.5, dev | 6 | 0 | 1 |
| Codestral 0.6, dev | 4 | 2 | 0 |
| qwen3.5 9B 0.3, dev | 5 | 3 | 1 |

qwen's held-out run had not finished a task when stopped. The 0.4/0.5/0.6 dev
comparison is incomplete, so no held-out run with a newer harness took place.
