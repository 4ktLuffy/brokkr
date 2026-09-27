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

---

# Night 2: 2026-09-27

**About timestamps:** at 01:03 I found I had been writing estimated times that
ran up to 40 minutes ahead of the clock. Night-2 entries up to then are corrected
and marked ≈; from here on, times come from `date`. Night-1 times were partly
estimated too and should be read as approximate order, not exact times.

**Where it started:** the first commit `5eb261b` is public at github.com/4ktLuffy/brokkr.
LICENSE (Apache-2.0, official text, sha256 `cfc7749b…`) is staged as its own
commit, awaiting approval. Nothing is committed or pushed tonight without
approval.

**Plan for the night:**
1. Finish the pre-registered 0.3.1 held-out baselines: Codestral's 55 remaining
   tasks, and qwen3.5 in order.
2. Finish the dev comparison with harness 0.6.1 (Codestral, all 10 dev tasks).
3. Give 0.6.1 a held-out run only if it beats 0.3 on dev, under a rule recorded
   beforehand.
4. Build a combined report that merges each model's result directories.

### 00:40 Resumed
- VM started; freetier proxy on 11501 (15M/day); UTC 09-26 usage 0.
- Built 0.6.1 from the committed source and installed it as a separate binary.
- plan.json amended with the resume rule before relaunching.

### 00:50 Six runs going; report merges columns
- Codestral 0.3.1 held-out: 3 shards covering 55 tasks. qwen 0.3.1 held-out, in
  order. Dev: 0.3.0 on its 2 missing tasks, and 0.6.1 on all 10. All six confirmed
  running on the expected first tasks.
- plan.json amended before any 0.6.1 run: 0.6.1 gets one held-out run only if it
  verifies strictly more dev tasks than 0.3.0 over the same 10.
- `scripts/report.py --col "Label=dir1,dir2"` merges runs across nights and shards.
  Infra rows for tasks later rerun are dropped; two scored rows for one task is an
  error, never a silent choice. Difficulty comes from `results/swe/difficulty.json`,
  taken from the dataset.

### ≈00:52 Second repository: SymPy (75 tasks)
- **Why:** Django-only was a stated limit. SymPy is the next largest repo in
  Verified, pure Python, and uses one Python version (3.9) throughout.
- **Parser:** a Go port of SWE-bench v4.1.0's `parse_log_sympy`. Verifier version
  0.2.0 is now recorded in every evidence file (0.1 had unittest and django).
  `crosscheck_parser.py` now picks the original parser named in each task's
  evidence. Still identical on Django logs.
- **`prepare.py` refactored** to per-repo configs from SWE-bench v4.1.0: Django
  (unchanged) and SymPy (mpmath==1.3.0, flake8, flake8-comprehensions;
  `bin/test -C --verbose <test files>`).
- **Regression check:** regenerating all 94 Django tasks' test commands, parsers and
  protected lists gives **0 differences**.

### ≈00:56 `brokkr bundle`: a review package with the evidence (the approval gate)
- **The gap:** the README promised verified fixes become draft PRs behind human
  approval, but nothing produced a PR.
- `brokkr bundle --run DIR --out DIR` writes `patch.diff` and `pr.md`. `pr.md` has the
  issue title, the agent's own summary, whether its claim matches the verdict, the
  required tests passing, and hashes of the repo, patch, patched tree, hidden test
  patch, kernel, rootfs and env, plus model, harness, turns, tokens and time.
- **It never pushes.** For a verified run it prints the `git apply` + `gh pr create
  --draft` command for a human to run. A run that did not verify is marked NOT
  READY and exits 1.
- **Tried on real runs:** Codestral 0.4 `14373` → READY (20/20 required); `14787` →
  NOT READY. Unit tests cover the ready path and an over-claim ("does not match").
- **A mistake I repeated:** a Python heredoc turned `\n` in Go strings into real
  newlines (the third time tonight). Caught by the compiler.

### ≈00:58 `scripts/make-report.sh` → `results/REPORT.md`
- Every column is defined by directory globs in one script, and night-1 + night-2
  and shard directories are merged per model and harness, so the report is
  reproducible.
- **Bug caught:** a glob with no matches (a column not run yet) made `ls` fail, and
  `set -euo pipefail` aborted the script silently with no file written. Fixed: an
  empty glob is an absent column.

### ≈01:02 SymPy prepared; a pass-rule question settled against SWE-bench
- **Prepared:** 75 tasks and 12 envs (one per SymPy version), all Python 3.9.25, 6
  packages each, 0 skipped. `prepare.py` took 7 min.
- **First validation, sympy__sympy-11618:** the gold patch passes all 5 required
  tests, but `bin/test` exits 1. `test_point` hits a RecursionError on Python 3.9 even
  with the gold patch, and **SWE-bench leaves it out of the required lists**; in
  SWE-bench's own setup this task counts as resolved. The parser cross-check agrees
  (SAME).
- **Verifier 0.3 adds a per-task `pass_rule`.** Default `exit_and_required` is
  unchanged, and every Django task and all Django results stay on it. SymPy tasks use
  `required_only`, SWE-bench's criterion: every required test passes and the exit
  code is ignored.
- **Tests through `verify.Run`:**
  - `required_only` passes on exit 1 when all required tests pass;
  - it still fails if a required test is missing, or if nothing is reported (the
    early-exit cheat);
  - the default still fails on exit 1;
  - an unknown rule is an error.
- **11618 now:** baseline FAIL, gold PASS.
- **SymPy split fixed before any validation or agent run:**
  `results/swe/split-sympy.json` (sha256 `8c1ae274…`), 10 dev and 65 held-out.
- **Validation of all 75** is running on verifier 0.3 and writes to its own
  `validity-sympy.jsonl`, so running Django evals never read a half-written line.

### 01:49 A client bug lost one task; fixed without touching the baseline
- **Symptom:** Codestral held-out `django__django-15382` (0.3.1, shard 2) ended as infra
  with "cannot unmarshal array into ... content of type string". Mistral sometimes
  returns `content` as a list of parts (`[{"type":"text","text":...}]`), and
  Brokkr's model client accepted only a string.
- **Fix:** `model.Message` accepts a string, null or a list of parts, keeping text
  parts and dropping "thinking" parts. Tests cover all forms, and check that tool
  calls survive and request encoding is unchanged.
- **The pre-registered 0.3.1 baseline keeps its original binary.** 15382 stays in its
  column as infra (unscored), with this cause.
- **Swapped at 01:49:56**, by atomic rename, the `brokkr-0.6.1` (Django dev) and
  `brokkr-v0.3` (SymPy) binaries for a build with the fix. Running tasks kept the
  old file; later tasks use the fix. It only changes how replies are decoded. The
  new 0.6.1 build also carries verifier 0.3, which leaves Django scoring unchanged
  (no pass_rule) and is recorded per evidence file.
- **Django dev, 0.3.0 complete:** 1/10 verified (the 2 missing tasks are now done,
  both FAIL).

### 02:03 A correct fix excluded by a false infra stop (kept excluded, and shown)
- qwen held-out `django__django-14580` (0.3.1): the final patch verifies PASS against
  the hidden tests, but the run was stopped as "context truncated". The reported
  prompt dropped by 26 tokens (13,471 → 13,445) at 13K of a 32K window: the
  false-positive rule that 0.6.1 fixes.
- The pre-registered rule makes infra runs unscored, so it does **not** count for
  qwen. The report now has an "of which PASS" column for infra runs, so the
  undercount is visible rather than hidden.

### 02:19 SymPy dev: the low score is real; one idea for 0.7
- **Instrument check first:** in 2 of Codestral's 3 SymPy dev failures *every* required
  test was missing (103/103, 11/11). That looked like a verifier problem, but it is
  not.
- In `sympy__sympy-13798` the agent's patch left a stray `}` in
  `sympy/printing/latex.py`, so SymPy cannot be imported and every test goes
  missing: a genuine FAIL.
- **Idea for 0.7 (not run tonight until tested on dev):** after every edit to a `.py`
  file, parse it with `ast.parse`, which parses and never executes code, and report
  a SyntaxError with its line in the tool result, so a broken edit is caught at once
  instead of at the end.
- 02:34: a second 0.3.1 baseline task lost to the same content-list bug: `django__django-15561` (shard 1). It is unscored, like 15382.

### 02:59 SymPy validation complete: 75/75 valid
- All 75 SymPy tasks pass the validity check (no patch → FAIL, gold → PASS) on
  verifier 0.3 with `required_only`. The parser cross-check agrees with SWE-bench's
  `parse_log_sympy` on all 150 logs.
- Together with Django's 92/94, **167 of 169 prepared SWE-bench Verified tasks are
  valid** in Brokkr's Firecracker setup.
- First SymPy dev pass: Codestral (h0.6.1) on `sympy__sympy-13877`.
- 03:21: 0.3.1 baseline `django__django-15499` stopped by the old truncation false positive (11,521 → 11,474 at 11.5K of 250K). Unscored under the rule; this flaw is fixed in 0.6.1.

### 03:29 Django dev comparison done; 0.6.1 qualifies for its held-out run
- Codestral dev, same 10 tasks: **0.3.0 1/10, 0.6.1 2/10** (0.6.1 passed 15731 and 14373). Under the rule recorded before any 0.6.1 run (strictly more than 0.3.0), 0.6.1 gets one Codestral held-out run on the 82 valid Django held-out tasks. It uses the client-fixed build, in 3 shards and split order, and is reported as its own column.
- **Caveat:** one task of difference on 10 is weak evidence. The held-out run, not the dev score, is what says whether 0.6.1 is better.

### 03:49 SymPy dev done (2/10); SymPy held-out started
- Codestral h0.6.1 on the 10 SymPy dev tasks: **2/10** (13877, 19346). Two runs stopped as stuck (13031, 15875); one patch left a syntax error (13798).
- As recorded before, the harness is not changed between SymPy dev and held-out. Held-out: 65 tasks, 2 shards, split order.
- Also running: Codestral h0.6.1 on Django held-out (82 tasks, 3 shards), the last task of the 0.3.1 baseline, qwen 0.3.1 in order, and next the 0.7 dev run.

### 03:53 Pre-registered baseline complete: Codestral h0.3.1, Django held-out
- **11 of 79 scored (14%)**. All 82 valid held-out tasks were run; 3 are infra and
  unscored (15382 and 15561, content-list client bug; 15499, old truncation false
  positive).
- **By SWE-bench difficulty:**

  | Difficulty | Verified |
  |---|---|
  | <15 min | 10/35 (29%) |
  | 15 min–1 h | 1/36 |
  | 1–4 h | 0/8 |

- **Over-claims:** the agent claimed a fix 40 times and was wrong 30 times (75%).
  Brokkr's verification caught every one; without it, 30 of 40 "fixes" would have
  been wrong.
- **qwen3.5-9B (local) h0.3.1, first 7 in order:** 2/7 scored, plus one verified
  PASS excluded as infra (14580, the false truncation stop).

### 04:20 Throughput is limited by Mac memory, not the VM's CPU
- VM load is low (1–3.7 on 8 vCPUs), but the Mac has 17% memory free and **18 GB of
  swap in use**. Most of the Lima VM is swapped out (0.76 GB resident).
- **Main competitor:** Henos's Colima `default` VM, an **x86_64 VM emulated in software
  (QEMU TCG) with 6 GB and 4 CPUs**, up 1d17h and running `lodgepg` (postgres:16)
  and `omotic-searxng`. Not Brokkr's, so **not stopped**. It is Henos's call: stopping
  it, or moving those services to the arm64 profile, would speed up Brokkr runs.
- The local qwen model takes 6 GB. I kept every pre-registered run as planned rather
  than pause one; runs are slower but unchanged.

### 04:35 Paired comparisons with an exact test (`scripts/compare.py`)
- Two runs are compared only on the tasks both scored. The verdict "is one better"
  rests on the discordant tasks, with an exact two-sided McNemar test. Checked
  against known values: 0 vs 5 → 0.0625, 1 vs 1 → 1.0, 0 vs 10 → 0.00195.
- `make-report.sh` now appends Codestral 0.3.1 vs 0.6.1, and Codestral vs qwen3.5
  (both harness 0.3.1).
- **Interim:** qwen 4/9 vs Codestral 1/9 on the same 9 tasks (3–0 discordant, p = 0.25).
  Codestral 0.6.1 4/14 vs 0.3.1 2/14 (2–0, p = 0.5). **Neither is a result yet**;
  both need more tasks.

### 05:36 Proxy was single-threaded: one lost task, and a likely cause of slow runs
- SymPy held-out `sympy__sympy-20154` ended as infra with "dial tcp ... :11501: i/o
  timeout": the agent could not connect to the freetier proxy.
- **Cause:** `tools/freetier_proxy.py` used Python's single-threaded `HTTPServer`,
  which serves one request at a time and queues at most 5 connections. With 7
  concurrent Codestral agents, requests were serialised behind each other, and
  connections beyond the backlog were dropped. This probably slowed every Codestral
  run tonight.
- **Fix:** a `ThreadingHTTPServer` with a backlog of 128. freetier's Ledger and Pacer
  share one `threading.Lock` and are thread-safe. A new test sends 20 concurrent
  requests, all answered in well under 10 s.
- The running evals keep proxy 11501, since restarting it would break about 7
  in-flight requests. A threaded proxy is up on **11502** for new runs.

### 05:45 Harness 0.7 on Django dev: 1/9 scored. No held-out run (rule)
- Codestral h0.7.0: 1 of 9 scored (14373), plus 1 infra (15280, model error). Even if
  that task passed on a rerun, 2/10 is not strictly more than 0.6.1's 2/10, so under
  the rule recorded beforehand **0.7 gets no held-out run**.
- **The syntax check itself is correct:** on 14787 it fired 25 times with the same
  real error ("expected an indented block after 'if' on line 52"; the original file
  parses under 3.12). Codestral kept editing elsewhere and never fixed it, ending
  with a 233-line patch. The check detects the problem; this model does not act on
  it.
- Harness ranking on Django dev with Codestral (10 tasks each; small samples):
  0.4 3/8 scored, 0.6.1 2/10, 0.6.0 2/5 (incomplete), 0.3.0 1/10, 0.7 1/9, 0.5 0/6
  (incomplete). No version stands out beyond noise; the one with a held-out run
  is 0.6.1.

### 05:46 CI workflow for the public repo
- `.github/workflows/ci.yml`: gofmt, go vet, go test and build; the Rust runner build (`--locked`); bash and Python syntax. No KVM is needed; Firecracker gates stay on the reference machine.
- Checked before writing it: the Go test binaries, compiled for linux/arm64, pass inside the VM; `cargo build --locked` succeeds in a separate target dir (the live runner binary untouched); all other steps pass locally.
- The freetier proxy tests are left out, with a comment, until freetier is published.

### 06:06 README results section, filled from results/ by a script
- The README has a 'Results on SWE-bench Verified' block. `scripts/fill-readme-results.py` fills it from the result directories (same globs as make-report, the same scoring, the same exact test), so no number in the README is typed by hand. It is marked in progress until the runs finish.
- **At 06:05:** Codestral 0.6.1 held-out 9/36 (25%) scored so far. Paired with 0.3.1 on 35 shared tasks: 9 vs 4 (5–0 discordant, p = 0.062). qwen3.5 vs Codestral on 13 shared tasks: 6 vs 1 (p = 0.062). SymPy held-out 4/25.

### 06:17 My own 15M cap parked Codestral; raised to 30M and resumed
- At 06:16 local all Codestral runs parked: **14.96M tokens in the first 3h16m of UTC
  day 09-27** (2096 requests, seven agents at about 4.6M tokens/hour). Mistral had
  not rate-limited once in four days (0 hits in the ledger). Parked runs stopped
  cleanly and were not scored, as designed.
- **The cap is raised to 30M/day,** with the measurement written into
  `dev/freetier/providers.yaml`. It is still self-imposed and below the reported
  ~1B/month; a real Mistral 429 parks cleanly.
- **Resumed per plan.json (amended before resuming):** every valid held-out task with
  no scored row, in split order. That is 42 Django tasks (0.6.1, 3 shards) and 38
  SymPy tasks (2 shards), through the threaded proxy on 11502.
- Caught while resuming: the SymPy resume directories (`...-n2-h061r-...`) would not
  have matched the report's SymPy glob and would have been silently left out. The
  globs in make-report and fill-readme are fixed.

### 07:08 The README stated something the data had outgrown; now generated
- At 07:08 both paired comparisons reached p = 0.031 (6–0 discordant each), while the template still said "neither is statistically clear". The note is now generated from the p-values.
- It says what they show and what they do not: with **two** comparisons the Bonferroni threshold is 0.025, so 0.031 is not below it; and these are **interim looks** at unfinished runs, which inflate false positives. **Provisional, consistent in direction, not settled.**

### 07:26 Stopped by request; resume tonight
Everything was stopped at 07:26: VM (11 agent/firecracker processes), both proxies,
and the local model. Runs cut off at that moment left no scored row. Under
plan.json's resume rule they are rerun, not counted.

**To resume tonight, in plan order, with the same binaries:**
- Codestral h0.6.1, Django held-out: 22 tasks left (82 valid).
- Codestral h0.6.1, SymPy held-out: 25 tasks left (65 valid).
- qwen3.5 h0.3.1, Django held-out: 64 tasks left, in split order. At about 30 min
  per task this needs several nights, or a rule for when to stop.

**Numbers at the stop** (`results/REPORT.md` and the README block are regenerated):

| Run | Scored | Verified | Notes |
|---|---|---|---|
| Codestral h0.3.1 Django (complete) | 79 | 11 (14%) | 30 of 40 "fixed" claims wrong |
| Codestral h0.6.1 Django | 60 | 13 (22%) | |
| qwen3.5 h0.3.1 Django | 18 | 9 (50%) | |
| Codestral h0.6.1 SymPy | 40 | 5 (12%) | |

- Paired: qwen vs Codestral 9 vs 3 of 18 (p = 0.031). 0.6.1 vs 0.3.1 13 vs 8 of 58
  (p = 0.125; it was 0.031 an hour earlier, which is why interim looks are
  provisional).

**Awaiting Henos:** commit 1 (LICENSE, staged); commit 2 (night-2 work, diff to show).
Nothing was committed or pushed tonight.

### 11:43 (day) Codestral resumed at Henos's request
- Remaining: 18 Django (0.6.1, 3 shards) and 24 SymPy (4 shards instead of 2, for speed), in split order, through the threaded proxy on 11502. plan.json was amended first.
- Budget: UTC 09-27 had used 20.96M of the 30M cap, so about 9M is left until 03:00; the remaining tasks need about 7M at the measured median of about 146K tokens per task. A park would stop them cleanly, and the cap is not raised without asking.

### 12:10 (day) Henos: pause qwen, finish Codestral
- qwen3.5 held-out is paused (not concluded) at 18 scored tasks; recorded in plan.json.
- Codestral: the current shards run until the 30M cap parks them. `scripts/resume-codestral.sh` reruns every valid task without a scored row, in split order with the same binaries. A background job starts it at 03:05 local, after the UTC reset, once the running shards have ended.
- The Mac is kept from idle sleep by `caffeinate -i -w <pid>`, tied to that job, which ends with it (no setting changed). A closed lid still sleeps.

### 12:52 (day) Codestral complete on both repositories
All valid held-out tasks now have a scored row: Django 82/82 and SymPy 65/65. That
used 27.6M tokens on UTC 09-27, under the 30M cap. The 03:05 resume job was not
needed and was cancelled, which also ended caffeinate. VM and proxy stopped.

| Run (held-out) | Verified | "<15 min" | "15 min–1 h" | harder | Fix claims | wrong |
|---|---|---|---|---|---|---|
| Django, Codestral h0.3.1 | 11/79 (14%) | 10/35 | 1/36 | 0/8 | 40 | 30 |
| Django, Codestral h0.6.1 | 18/82 (22%) | 16/36 | 2/38 | 0/8 | 19 | 10 |
| SymPy, Codestral h0.6.1 | 7/65 (11%) | 6/23 | 1/35 | 0/7 | 18 | 13 |
| Django, qwen3.5-9B h0.3.1 (paused) | 9/18 (50%) | | | | | |

- **Paired, Django, 0.6.1 vs 0.3.1 on the 79 shared tasks:** 18 vs 11. The 8–1
  discordant split gives exact McNemar p = 0.039: below 0.05 on its own, but not
  below the Bonferroni 0.025 for the two comparisons made.
- **Reading:** the harness changes (reproduce-first prompt, loop detection, a
  truncation check that no longer fires falsely) point to more fixes **and** fewer
  false claims (19 claims, not 40; 10 wrong, not 30). Not proven at the 0.025 level.
- **qwen3.5 vs Codestral (first 18 tasks, qwen paused):** 9 vs 3, p = 0.031.
  Provisional.

---

# pydantic: a real-world, uncontaminated task set (day, 2026-09-27)

### 13:38 Why pydantic, and how tasks are chosen
- **Why:** top-tier engineering; the validation layer under FastAPI, the OpenAI and
  Anthropic SDKs, LangChain and freetier; not in SWE-bench. Live GitHub counts:
  201 open and 1000+ closed "bug V2" issues.
- **Uncontaminated by construction.** Only fixes merged **on or after 2026-03-01**:
  Qwen3.5-9B was published on Hugging Face 2026-02-27, and Codestral is
  `codestral-2508` (its alias in Mistral's model list).
- **Candidates:** closed "bug V2" issues fixed by exactly one merged PR into main
  that changes `pydantic/` and `tests/`. 451 in all, **48 merged since 2026-03-01**
  (`results/pydantic/candidates.json`).
- **Task construction** (`scripts/gh/prepare.py`, generic, with a per-repo config):
  - base = first parent of the PR's merge commit;
  - test.patch = the PR's `tests/` changes (hidden);
  - good.patch = the rest (reference fix);
  - env = the base commit's `uv.lock` (dev group), Python 3.12.
- **Required tests, derived as SWE-bench does** (`scripts/gh/derive_tests.py`):
  FAIL_TO_PASS = passing only with the gold fix; PASS_TO_PASS = passing both
  times. A task with no FAIL_TO_PASS is unusable.

### Problems found and fixed on the way
- **pydantic-core is in the repo (Rust).** 8 of the 48 fixes change it and are
  excluded: verifying them would mean compiling Rust on every sandbox run.
- **Released core wheels do not work.** I first installed the PyPI wheel for each
  base's core version (5 versions instead of 24 builds). The very first task
  failed to import: the base's Python code calls `_schema_gather`, which no
  release has. The core is now **compiled from the base commit's own source**,
  one env per distinct core tree, with builds sharing one cargo target dir.
- **I nearly wiped the interpreter I was running on.** `uv run` picked the Python
  inside `/opt/env`, which env building empties. `prepare.py` now refuses to run
  from under `/opt/env`, and uses the system Python.
- **A verifier hole:** under `pass_rule: required_only`, an empty required list
  gave PASS for anything. Verifier 0.4.1 returns ERROR for a patched run with no
  required tests (tested).
- **That guard then blocked derivation**, whose gold run has no required tests yet.
  A separate `--collect-only` mode records statuses with no verdict (COLLECTED),
  so the guard stays intact for real verdicts (tested).
- `pytest-pretty` rewrites pytest's summary. It is disabled with `-p no:pretty`;
  its entry point is `pretty`, read from its dist-info, not guessed.
- SWE-bench's grading counts XFAIL as passing; confirmed in v4.1.0 `grading.py`
  and ported.
- **First task (13780):** it builds and runs, but all 5 of its tests only run on
  free-threaded Python, so it is correctly marked unusable.

### 14:56 pydantic task set ready; Codestral running
- **Derived:** 37 tasks built (the other 3 PRs changed no test_*.py file), 29 with at
  least one FAIL_TO_PASS test.
- **Duplicates removed:** 3 PRs each closed several issues (#13672, #13459, #12785),
  so one task is kept per PR (lowest issue number): **26 unique tasks**
  (`results/pydantic/tasks.json`).
- **Validity:** **26/26 valid** (no patch → FAIL, gold → PASS), and SWE-bench's pytest
  parser agrees with Brokkr's on all 52 logs. The median gold test run is 66 s.
- **Environments:** 26 distinct pydantic-core trees compiled from source (shared cargo
  cache).
- **Plan fixed before any agent run** (`results/pydantic/plan.json`, sha256
  `0a0a5704…`): evaluation-only, all 26 held out, sha256 order, Codestral with
  brokkr-v0.4 (harness 0.7.0, verifier 0.4.1), same budgets.
- **Running:** `scripts/gh/run-codestral.sh pydantic r1` (3 shards). About 2.4M tokens
  remained today, roughly 10 tasks, so a park is likely. The same script resumes
  after 03:05 (r2); caffeinate is tied to the whole chain.

### 15:40 Weakness map, and harness 0.8 aimed at the biggest weakness
- `scripts/analyze_failures.py` classifies every scored real-task run (321: 65 pass,
  256 fail) from its own patch, the gold patch and the final evidence. Written to
  `results/ANALYSIS.md`.
- **Biggest weakness: never editing** (95 runs, 37% of failures, from hand-checked
  samples):
  - 48 runs reproduced over and over, then submitted nothing (43% of their calls
    were run_python);
  - 47 runs read and searched until the turns ran out (82% of their calls).
  - The rest: incomplete fix 22%, wrong location 12%, broke existing tests 10%.
  - Multi-file fixes: 1/39 solved. A reproduction helps: 24% pass with one, 10%
    without.
- **Harness 0.8.0:**
  - a turn and sandbox budget footer on every tool result;
  - on hidden-test tasks with no edit yet, a commit nudge at a third of the turns,
    at two thirds, and after 3 run_python attempts (each at most once);
  - prompt: commit early and refine, and a fix may need the same change in several
    places.
  - Tests: 3 reproductions give exactly 1 nudge; an edit first gives none; visible-
    test tasks get none. The first version of the test failed because the helper
    allows 1 sandbox run, so the rule now counts attempts.
- **Pre-registered test:** Codestral 0.8 on the same 20 dev tasks where 0.6.1 got 4/20.
  A held-out run only if strictly more than 4/20, and only with Henos's OK. Caveat
  recorded: the analysis included 14 pydantic runs.
- **Scheduled:** after the pydantic r2 resume (03:05) finishes, 4 workers on the 20
  dev tasks. caffeinate is tied to both chains.

### 16:18 Live mode (harness 0.9.0): fix an open issue and prove it
- Henos stopped the chains waiting for the Mistral reset (pydantic r2 and the 0.8
  dev run). Nothing ran after the r1 pass. The 0.8 pre-registered test has not run.
- **Live mode:** the agent must write a regression test (new
  `tests/test_brokkr_*.py`) and fix the code. `verify.SelfTest` checks it with two
  microVM runs: A (original + new test: must fail), B (whole patch: must pass, and
  every existing test that passed in A must still pass).
- **Agent 0.9.0:**
  - `create_file`, offered only on live tasks: new files only; under a protected
    path, only a new `test_brokkr_*.py`;
  - live prompt paragraph; `run_tests` includes the new test files;
  - non-live tasks get the same tools and prompt as 0.8.0.
- **Other pieces:** `brokkr selftest`, which judges any patch the same way;
  `scripts/gh/live_task.py`, which builds a task from an open issue at HEAD, or a
  live variant of a historical task; the bundle shows the self-test evidence.
- **Checked in real microVMs** on pydantic 13692 with a hand-written test:
  - fix + test: PASS;
  - test only: FAIL;
  - fix only: FAIL.
  - Unit tests cover the rest: a test that passes before, a regression, a protected
    edit (REJECTED), overwriting an existing test, a new test not named
    `test_brokkr_*`, and conftest.py.
- **Mistake caught:** the first live baseline (#13754 at HEAD) ran `pytest tests/`.
  Collecting `tests/pydantic_core` (needs hypothesis) stopped pytest with 0 tests
  run, so "no regressions" would have been vacuous. Fixed in two ways:
  - The regression set is now `tests/test_*.py` (4954 ids, 93 s).
  - A self-test whose run A passed no existing test is an ERROR, and the agent
    refuses such a baseline before spending any turns.
- **Found:** SWE-bench's pytest parser cuts ids at the first space. 280 passing
  lines became 277 ids on test_types.py. Kept for fidelity, and listed in Known
  limits.
- **Also:** my first three edit scripts for agent.go never wrote the file (missing
  write), and the build caught it. Re-applied.
- **Not run:** live mode with a model (no Codestral until Henos says). Next: the
  26 pydantic tasks as live variants, to see whether the agent's own test agrees
  with the hidden tests.

### 18:06 Model router (closes the "vLLM/Bedrock routing" gap with free backends)
- `internal/route`:
  - **Per task (Pick):** backends ordered by estimated pass rate on the task's
    repo, from `results/*/runs.jsonl`. The repo record is shrunk toward the
    backend's all-real-repo record (weight 4 runs). Unscored and mixed runs are
    left out. Backends that don't answer are skipped.
  - **Per call (Router.Chat):** parked, 429, 5xx or unreachable backends fall back
    to the next one mid-run. A conversation that outgrows a window escalates to
    a bigger one. Malformed replies and other 4xx are returned, not routed around.
    If everything is down and something was parked, the error is parked (exit 4).
  - Foreign tool-call ids are rewritten to 9 alphanumerics (Mistral's rule), and
    only when another backend is sent the conversation.
- **Agent:**
  - talks to a `Chatter` interface;
  - the overflow check uses the router's largest window, and the near-window
    truncation rule uses the current backend's;
  - the prompt-count baseline resets on a switch (different tokenizer, so a
    smaller count is not truncation);
  - the summary records `served_by` and `route_switches`.
- **Reports:** `report.py` and `compare.py` drop mixed-backend runs.
- **Mistakes caught by looking at real output:**
  - Codestral showed as "down" because the freetier proxy answers 404 on `/models`.
    Now only transport errors and 5xx count as down.
  - A flat Beta(1,1) prior put an untried backend (50%) above Codestral's known
    12% on sympy for the wrong reason. It is now shrunk toward each backend's own
    record.
  - A test fake printed a counter into JSON through `%.0d`, which is not empty for 1.
- **Real smoke test** (local only, no Codestral tokens): routed `brokkr fix` on the
  calc fixture. The router picked qwen from the toy record (13/14 vs 13/15), and
  qwen passed in 5 turns; `served_by` and the sampling were recorded.
- **Not yet shown for real:** a mid-run fallback or escalation against live
  servers (fake-server tests only). No routed evaluation has been run.
