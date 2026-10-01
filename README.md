# brokkr

Autonomous coding agents that run inside Firecracker microVMs, fix failing builds and
issues, and open a pull request with the evidence attached.

In Norse myth, Brokkr forged Mjölnir while Loki, as a fly, stung him to spoil the work.
The hammer came out whole anyway. That is the design brief: the agent's code is
untrusted, the task may be adversarial, and the result still has to be sound.

> **A patch is only evidence if the verifier can be shown to reject a broken one.**

Status: early. The verifier and a first agent work on small fixture tasks with a
local 7B model. Nothing below is claimed unless a script on disk reproduces it.

## Design

- **Go control plane.** Task intake (failed CI jobs, GitHub issues), queue, sandbox
  scheduling, the approval gate, and PRs opened as drafts.
- **Rust sandbox runner.** Boots and restores Firecracker microVMs and runs the agent
  inside the guest behind seccomp, cgroup quotas, per-task network policy and
  short-lived credentials.
- **Verify before anything leaves the sandbox.** Tests, type checks, lint and a
  security scan. Deliberately broken patches (negative controls) must fail them before
  a green result counts.
- **An evidence bundle for every run.** Repro command, diff, logs, trace and hashes,
  enough for a reviewer to re-run the verdict.
- **Humans approve every merge and every destructive action.** The agent can only
  propose.
- **Pluggable models.** Local (Ollama, vLLM) or hosted, routed by task size.

## Gates passed

| Gate | What it proves | Instrument |
|---|---|---|
| 0 | This host can boot a Firecracker microVM; the check fails without `/dev/kvm` | `scripts/kvm-smoke.sh` |
| 1 | The verifier goes red: of six patches, only the real fix passes | `scripts/gate1.sh` |

Gate 1 on the reference machine:

| Case | What the patch does | Verdict | Caught by |
|---|---|---|---|
| none | nothing; the bug is still there | FAIL | tests fail |
| good | the real fix | **PASS** | (none) |
| broken | a plausible fix that is wrong for most inputs | FAIL | tests fail |
| tamper | edits the tests to expect the buggy value | REJECTED | protected files changed; no VM is booted |
| early-exit | calls `os._exit(0)` on import, before any test runs | FAIL | required tests missing |
| forge | tries to overwrite the sandbox result and reach the network | FAIL | write denied, network unreachable, tests fail |

The early-exit case is the reason the required-test rule exists. Its test command
exits **0** with no output, so a verifier that only looks at the exit code, the usual
approach, would record a pass. Remove the rule (`required_tests: []`) and it does.

## How a patch is verified

1. The repository is copied and the patch applied with `git apply`, which refuses
   absolute paths and `..`.
2. Every file under the task's protected paths is hashed before and after. Any
   difference rejects the patch, whatever its diff headers say.
3. The patched copy goes onto a throwaway ext4 drive and a fresh microVM boots
   with it. `brokkr-init` (PID 1, in the read-only root image) runs the test
   command as `nobody`, with no network device, under a timeout.
4. Only `brokkr-init` can write the result, and the runner reads it from the drive
   after the VM has exited.
5. PASS requires exit 0 **and** a reported pass for every required test.
6. `evidence.json` records the verdict, the reasons, hashes of the repo, patch and
   patched tree, the per-test results, the kernel, rootfs and Firecracker versions,
   and the machine it ran on.

## The agent

`brokkr fix` gives a model a task (an issue, and for practice tasks the failing
output) and these tools:

| Tool | What it does | Runs code? |
|---|---|---|
| `list_dir`, `search`, `read_file` | look around a large repository: one directory at a time, regex search (50 hits), line ranges (300 lines) | no |
| `replace_in_file` | exact text replacement, tolerant of a uniformly wrong indent | no |
| `replace_lines` | replace a numbered line range (0.5+) | no |
| `run_tests` | runs the tests on the current diff in a fresh microVM; on SWE-bench tasks, only the visible ones | in the sandbox only |
| `run_python` | runs a Python program from the repository root in a fresh microVM (to reproduce the issue); never part of the diff | in the sandbox only |
| `submit` | ends the run, with the agent's claim that it fixed the task | no |

Edits refuse protected paths (the tests) and paths outside the repository. The
harness records its version (`HarnessVersion` in `internal/agent/agent.go`, with a
changelog), and results from different versions are never pooled. Other safeguards:

- **Loops end the run, and it is scored.** Six identical failing calls in a row, or
  five identical calls with no edit in between, stop the run as `stuck` (0.5, 0.6).
- **A claimed fix needs a reproduction.** On hidden-test tasks, `fixed=true` with no
  `run_python` since the last edit gets one reminder (0.4+).
- **Output that fails to parse is the model's failure.** A malformed model reply is
  resampled twice and then scored as a failure, not excused as infrastructure.

The model never runs code on the host. When it stops, Brokkr takes the final diff
and verifies it again from scratch. That verdict is the result; the agent's claim is
recorded next to it, so **over-claims** (said fixed, wasn't) are counted rather than
hidden. Each run leaves `transcript.jsonl`, `final.patch`, the evidence for every
sandboxed test run, and `summary.json`.

Models are reached over the OpenAI-compatible chat API: Ollama, vLLM, llama.cpp or
a hosted provider (`--model-url`, `BROKKR_API_KEY`).

`scripts/eval.sh` runs the agent K times per task. Before a task is scored, it
checks that the unpatched repo FAILS and the reference patch PASSES; a task that
fails either check is reported invalid and excluded.

## From a verified run to a pull request

`brokkr bundle --run RUN_DIR --out DIR` turns a finished `brokkr fix` run into what
a reviewer needs:

- `patch.diff`;
- `pr.md`: the issue title, the agent's own summary, whether its claim matches the
  verdict, the required tests passing, and hashes of every input (repo, patch,
  patched tree, hidden test patch, kernel, rootfs, env).

Brokkr never pushes, opens or merges anything. For a verified run it prints the
`git apply` and `gh pr create --draft` commands for a person to run. A run that did
not verify is marked NOT READY. That is the approval gate.

## Live mode: fixing issues nobody has written tests for

Benchmarks come with hidden tests. Real open issues do not. In live mode the agent
must also write the proof: a regression test in a new `tests/test_brokkr_*.py`
file. Brokkr then checks that proof itself, in two fresh microVMs:

| Run | Contents | What must happen |
|---|---|---|
| A | original code + the agent's new test file | the new test fails |
| B | original code + the whole patch | the new test passes, and every existing test that passed in A still passes |

A test that already passes without the fix proves nothing, so it cannot be the
evidence. Existing tests stay read-only: `create_file` makes new files only,
and under `tests/` only a new `test_brokkr_*.py`. The self-test rejects any
other change to a protected path. If no existing test ran in A (a collection
error, say), the result is ERROR rather than PASS: "no regressions" among zero
tests means nothing.

```bash
/usr/bin/python3 scripts/gh/live_task.py issue pydantic/pydantic 13754   # in the VM
brokkr fix --task ~/.cache/brokkr-gh/live/pydantic__pydantic-13754/task.json ...
brokkr bundle --run RUN_DIR --out DIR     # pr.md carries the self-test evidence
brokkr selftest --task T.json --repo DIR --patch P --out DIR   # any patch, no model
```

Checked in real microVMs on pydantic#13692, with a hand-written test and the
maintainers' fix:

| Patch | Verdict |
|---|---|
| fix and test | PASS: 1 new test fails before and passes after, no regressions |
| test only | FAIL: the new test does not pass |
| fix only | FAIL: no regression test |

For a live task at pydantic's HEAD, the regression set is pydantic's own suite
(`tests/test_*.py`): 4954 test ids in 93 s per run. `live_task.py variant`
makes a live copy of a historical task that keeps its hidden tests. A run of
that copy records both verdicts, which measures whether the agent's own test
agrees with the maintainers' tests.

## Sandbox slots: measured, not guessed

The plan was snapshots (restore a booted microVM) and a pool of parallel VMs.
Measurements on the M4 Mac changed it:

- **Per-run overhead is small.** A Django run spends about 2.8 s outside the
  tests: 1.1 s boot, 0.9 s `chown`, 0.6 s shutdown, 0.2 s host-side. A
  snapshot could save about 1 s of a roughly 50 s run, and restored VMs would
  share memory and random-number state. Not built.
- **Nested virtualization gives all microVMs about one core together.** 8
  CPU-bound microVMs each ran 8x slower. One microVM with 4 vCPUs ran 4 jobs
  no faster than with 1. Plain processes in the Lima VM do scale.
- **So too many VMs at once only makes each one slower.** This explained the
  22 s median "overhead" in past runs, which ranged up to 90 s.

Brokkr now caps microVMs running at once, host-wide, across processes, with
lock files (`BROKKR_SANDBOX_SLOTS`, default 2 in `scripts/env.sh`). A run over
the cap waits, and the wait is recorded as `sandbox_queue_ms`, apart from the
VM time. 8 concurrent real Django verifies:

| | Batch | VM time per run | Wait |
|---|---|---|---|
| no cap | 457 s | 448 s | 0 s |
| 2 slots | 339 s | 82 s | 123 s |

On a bare-metal Linux server, raise the cap toward the core count. That is
where a VM pool pays off.

## Beyond Python: Go and C++ repositories

Brokkr now verifies fixes in Go and C++ repositories from SWE-bench
Multilingual (`scripts/ml/prepare.py`). Its log parsers (gotest, cargo,
googletest, jq, redis, micropython) are ports of SWE-bench's own parsers,
checked against the originals' output on sample logs.

- **Go:** one environment drive per repository with every Go version and
  module its tasks need, so the guest runs offline (`GOPROXY=off`,
  `GOTOOLCHAIN=local`). A warm build cache is symlinked into the guest. That
  takes 3.5 s; copying it took 123 s. Building the cache compiles test
  binaries with `go test -c` and never runs them outside a microVM.
- **C/C++:** a second guest image with gcc 13, cmake, Tcl and autotools
  (`scripts/build-rootfs-cc.sh`), chosen per task (`"rootfs"`). Its hash is
  recorded in the evidence. The build runs inside the microVM.

Validity (unpatched FAILs, maintainers' fix PASSes):

| Repo | Tasks | Valid | VM time per gold run |
|---|---|---|---|
| gin-gonic/gin (Go) | 8 | 8 | 2.4–8.8 min |
| fmtlib/fmt (C++) | 2 checked of 11 built | 2 | 16–17 min (cold build on one shared core) |

Not done yet: the other Go repos (caddy, prometheus, hugo, terraform) need
gigabytes of modules and the VM disk is nearly full; jq and redis build
during SWE-bench's image setup, which is not in the dataset; Rust; any
model run on these tasks.

## Flight recorder

`brokkr replay --run RUN_DIR --out replay.html` turns a finished `brokkr fix`
run into one self-contained HTML page with no network requests. It shows:

- the verdict, and the agent's claim against it, with over-claims highlighted;
- every turn, with tool calls, edits as diffs, and sandbox runs with their
  log tails;
- harness interventions;
- a chart of prompt size, a tools-over-time strip, the final patch and the
  evidence.

Run data is embedded as escaped JSON and drawn with `textContent` only, so
hostile transcript text cannot run script. `--compare B` shows two runs side
by side. Samples are in `docs/replays/`.

## Does the agent's test pin the fix?

`brokkr mutate --task T.json --repo DIR --patch P --out DIR` breaks the fix in
small ways and runs only the agent's new tests against each broken version in
a fresh microVM. Mutations: revert a hunk, flip a comparison, swap and/or,
negate a condition, change an integer by one, return None, empty a string,
delete a statement.

- A mutant is killed when a new test that passed with the real fix no longer
  passes.
- Surviving mutants are listed in `mutate.md`: they show what the test does not
  check.
- On pydantic#13460, the maintainers' tests killed 8 of 8 non-equivalent
  mutants. A weaker test killed 7 of 8, missing the fallback for unknown types.
- A score measures how much of the fix the test pins down, not whether the fix
  is right.
- `brokkr bundle` shows the score when `mutate.json` is present.

## Patch minimizer

`brokkr minimize --task T.json --repo DIR --patch P --out DIR [--max-runs N]
[--granularity hunk|line]` shrinks a verified patch to the smallest one that
still verifies. It runs Zeller's ddmin over hunks (and with `line`, then over
changed lines), using the real verifier in fresh microVMs as the oracle:

- The full patch must PASS first.
- A subset that no longer applies counts as FAIL without booting a VM.
- Verdicts are cached by patch hash.
- If the run budget ends first, the best verified patch is returned, marked as
  not proven minimal.

Measured:

- A calc fix bundled with a debug print, a comment, an unused helper and a
  stray file went from 10 changed lines to the 2-line fix, in 15 VM runs.
- A past Codestral PASS patch on sympy-19637 went from 8 changed lines to 2.
- Line mode on real patches has not finished a run yet.

## Code tools (optional)

`--code-tools` adds `find_definition`, `find_usages` and `outline`. They are
answered from an ast index built with python3 on the host, which never
imports or runs repository code. After each `.py` edit, a static check
reports only problems the edit introduced: undefined names, use before
definition and duplicate arguments. Offline results:

- On the patched files of 209 historical tasks (285 files), the check reported
  nothing.
- `find_definition` found 402 of 403 gold-modified functions by qualified
  name.
- `find_usages` pointed at another changed file in 22 of 33 multi-file fixes.

No model has been run with the tools yet.

## Model routing

`brokkr fix --routes dev/routes.json` sends the agent's model calls through a
router instead of a single model:

- **Per task, pick by evidence.** Backends are ordered by estimated pass rate on
  the task's repository, from every scored run in `results/`. A backend's record
  on this repository is shrunk toward its record on all real repositories,
  weighted as 4 runs, so one lucky run doesn't win. A repository nobody has
  tried is judged by each backend's general record. Backends that don't answer
  are skipped. `"objective": "pass_per_hour"` also weighs speed.
- **Per call, fall back.** A parked free tier, a rate limit, a 5xx or an
  unreachable server moves the conversation to the next backend. The switch
  happens mid-run, with no restart.
- **Escalate.** A conversation that outgrows a backend's window (the local
  model's 32K) moves up to a larger one (Codestral's 250K) instead of stopping.
  The router never moves a conversation into a window too small to hold it.
- **Not routed around:** a malformed reply (the model's failure, scored) and
  other 4xx errors (a bug in the request).
- **Tool-call ids** made by one backend are rewritten, only when another
  backend is sent the conversation, to the 9-character form every provider
  accepts. Mistral rejects Ollama's.

`brokkr route --for owner/repo` shows the evidence and the order. With
today's results:

| Repository | Local Qwen 3.5 9B | Codestral | Order |
|---|---|---|---|
| django/django | 12/23 (est. 52%) | 38/209 (18%) | Qwen, then Codestral |
| sympy/sympy | untried (52% from its record) | 9/75 (12%) | Qwen, then Codestral |
| pydantic/pydantic | untried (52%) | 6/14 (37%) | Qwen, then Codestral |

These estimates are for choosing a backend, not an evaluation: they pool
harness versions and task sets, which the evaluation never does. Every
routed run records `served_by` and each switch. A run served by more than one
backend is a mixed run, and `report.py` and `compare.py` leave it out of model
comparisons.

## Free-tier models through freetier

Hosted free tiers run through [freetier](https://github.com/4ktLuffy/freetier), via a
small proxy on the Mac (`tools/freetier_proxy.py`). The agent in the VM talks to it as
an ordinary OpenAI-compatible endpoint and never holds a key.

- **Pacing and one shared ledger.** Every call is paced to the provider's published
  limits and booked in freetier's machine-wide ledger, shared with any other project
  on the machine that uses the same key.
- **A spent allowance stops the eval; it is never scored.** When the allowance is gone,
  freetier parks the call instead of retrying into 429s. The proxy answers
  `budget_parked` with the UTC time the allowance returns, `brokkr fix` exits 4, and
  `scripts/eval.sh` stops, writes `PARTIAL` and scores nothing from the parked run.
- **The key stays on the Mac.** It comes from `BROKKR_UPSTREAM_API_KEY` or the macOS
  Keychain, the proxy binds `127.0.0.1` only, and nothing logs the key.

```bash
security add-generic-password -s brokkr-upstream -a "$USER" -w     # once; prompts for the key
BROKKR_UPSTREAM_URL=https://openrouter.ai/api/v1 tools/.venv/bin/python tools/freetier_proxy.py
# in the VM:
BROKKR_MODEL_URL=http://host.lima.internal:11500/v1 BROKKR_MODEL=<model> scripts/eval.sh
```

**Which free tier can carry an agent.** Every turn resends the whole conversation, so
an agent loop spends tokens quickly. On the five fixtures, 15 runs used 507K tokens
(median 25K per run), and the largest single request was 7.2K tokens. Against
freetier's limits table:

| Provider (free) | Limits | Fits? |
|---|---|---|
| Groq | 8K tokens/min, 200K tokens/day | No. 15 toy runs are 2.5 days of allowance, and real-repo requests exceed the per-minute cap, so they can never be sent |
| Cloudflare Workers AI | 10K neurons/day (about 300K input tokens) | No |
| OpenRouter `:free` models | 20 req/min, 50 req/day; no token caps | About 4 runs a day: enough to test the setup |
| OpenRouter `:free`, after $10 of credit has ever been bought | 20 req/min, 1000 req/day | About 90 runs a day: the only free route that fits |

Tests: `tools/.venv/bin/python -m unittest tools/test_freetier_proxy.py` runs with no key
and all outbound traffic routed to a dead address, using a throwaway ledger.

## Results on SWE-bench Verified

<!-- RESULTS:START -->
Snapshot taken 2026-09-27 12:52 local from `results/REPORT.md` (regenerate with
`scripts/make-report.sh`). The counts below are
final for Codestral; qwen3.5 is paused. Every verdict is Brokkr's own check against SWE-bench's hidden
tests. Held-out tasks were never used to shape the harness (`results/swe/plan.json`).

**Django 4.x, held-out (82 valid tasks):**

| Run | Verified | Notes |
|---|---|---|
| Codestral, harness 0.3.1 (pre-registered baseline) | **11 / 79 (14%)** | complete. 29% of "<15 min" tasks, 1 of 36 "15 min–1 h", 0 of 8 harder. **Claimed a fix 40 times; 30 were wrong** |
| Codestral, harness 0.6.1 | **18 / 82 (22%)** | complete |
| qwen3.5-9B, local on the Mac, harness 0.3.1 | **9 / 18 (50%)** | paused after the first 18 tasks in pre-registered order |

**Paired, on the same tasks (exact McNemar test):**
- Codestral 0.6.1 vs 0.3.1: 0.6.1 18/79 vs 0.3.1 11/79 on 79 shared tasks (8–1 discordant, p = 0.039)
- qwen3.5-9B vs Codestral: qwen3.5-9B 9/18 vs Codestral 3/18 on 18 shared tasks (6–0 discordant, p = 0.031)

Neither comparison is below the Bonferroni threshold (α = 0.025 for 2 comparisons), though the direction is consistent. The qwen3.5 comparison covers only its first 18 tasks (paused) and was looked at repeatedly, so treat it as provisional.

**SymPy, held-out (65 valid tasks):** Codestral, harness 0.6.1: **7 / 65 (11%)**, complete.

Across both repositories, 167 of the 169 prepared tasks passed the validity check:
Django 92/94 and SymPy 75/75. The Brokkr ports of SWE-bench's log parsers agree with
the originals on every validation log (338 logs).
<!-- RESULTS:END -->

## First results (toy tasks, local 7B model)

`brokkr-qwen2.5-7b-16k` (qwen2.5 7B Instruct, Q4, via Ollama, 16K context) on the
five fixtures, 3 runs each, temperature 0, on the reference machine. The same model
and tasks were run with the original exact-match edit tool (`strict`) and with the
indentation-tolerant one (`reindent`):

| Task | strict: PASS | reindent: PASS | reindent: over-claims |
|---|---|---|---|
| calc | 3/3 | 3/3 | 0 |
| inventory | 0/3 | 1/3 | 0 |
| lru | 0/3 | 2/3 | 1 |
| slugify | 0/3 | 0/3 | 3 |
| workdays | 0/3 | 0/3 | 1 |
| **Total** | **3/15** | **6/15** | **5 of 11 claims** |

How to read it:

- **This is a small sample, not a benchmark.** At temperature 0, repeated runs are
  close to duplicates, so there are closer to 5 independent data points than 15.
  3/15 against 6/15 is a direction, not a significant difference.
- **The verifier is what makes the agent usable.** With the reindent tool, the
  agent claimed a fix 11 times and was right 6. Without independent
  verification, 5 of 11 pull requests would have been broken code presented as a
  fix. In one slugify run the agent saw its own tests fail twice, hit the test
  budget and submitted `fixed=true` anyway.
- **An earlier run is invalid and kept as a record**
  (`results/20260924T115935-qwen2.5_7b/INVALID.md`). Ollama served the model with a
  4K window and silently truncated prompts; runs in the valid evals reached 7.2K
  prompt tokens. The agent now stops such runs as infra errors
  (`scripts/probe-context.py` shows the truncation).

These runs used harness 0.1. Two issues they exposed are fixed in 0.2 (see
`HarnessVersion` in `internal/agent/agent.go`): a `run_tests` call with no changes no
longer spends the test budget, and the budget-refusal message no longer tells the
model to "submit now". Harness 0.2 also records `claim_against_own_tests`: a "fixed"
claim made when the agent's own last test run had not passed. Results from different
harness versions are not pooled.

## Real tasks: SWE-bench Verified in Firecracker

`scripts/swe/prepare.py` turns SWE-bench Verified instances into Brokkr tasks. So far
that means Django 4.0, 4.1 and 4.2: 94 tasks.

- **Environments.** One read-only ext4 image per repo version, mounted at `/opt/env`
  in the guest. Each follows SWE-bench **v4.1.0**'s specs (pinned; current SWE-bench
  no longer carries them in code) with two recorded differences: Python comes from
  uv's standalone builds, since there is no conda on arm64, and the package is not
  installed, so the working copy is imported through `PYTHONPATH`.
- **Hidden tests, as in SWE-bench.** The agent sees only the issue text. The verifier
  applies SWE-bench's test patch itself, after the candidate patch, and requires every
  FAIL_TO_PASS and PASS_TO_PASS test to pass. The agent's own `run_tests` runs the
  repository's existing tests only.
- **Same test names as SWE-bench.** Test logs are read by a Go port of SWE-bench's
  `parse_log_django`. `scripts/swe/crosscheck_parser.py` runs the original on every
  validation log and compares the results.
- **Task validity.** `scripts/swe/validate.sh` requires, per task: no patch → FAIL,
  gold patch → PASS, and parsers agree. Invalid tasks are listed, never scored.
- **Dev/held-out split.** `results/swe/split.json` was fixed before any agent ran: 10
  dev tasks for debugging the harness, 84 held-out. It was chosen by
  sha256(instance_id).
- **Sandbox.** The guest has loopback only, for test servers on localhost, and no
  network device.

## Known limits

Stated plainly, so nobody has to find them:

- **Output forgery by code under test.** The code under test runs in the same
  process as the test runner, so a patch written specifically to print fake
  "... ok" lines could fool the parser. It would also be an obvious diff for the
  human who approves every merge. A trusted out-of-process test reporter is planned.
- **Standard-library shadowing.** `python3 -m unittest` puts the repo first on
  `sys.path`, so a patch adding `unittest.py` could replace the runner. Protecting
  such names, or running tests from a trusted harness, is planned.
- **No jailer, cgroup quotas or snapshots yet.** Firecracker's default seccomp
  filters apply. The jailer, cgroups v2 limits and snapshot restore come next.
- **Two log formats so far.** Python `unittest` and Django's runner (a port of
  SWE-bench's parser). pytest, Go and Cargo come later.
- **One repository so far.** The SWE-bench pipeline covers Django 4.x. Other repos
  need their own environment specs and, for pytest-based ones, a pytest parser.
- **Live mode trusts the agent's test to be about the issue.** Brokkr proves the
  test fails without the fix and passes with it; it cannot prove the test checks
  what the issue asks for. The reviewer reads the test. Live mode has no model
  results yet.
- **pytest ids with spaces are cut at the first space** (as in SWE-bench's
  parser, kept for fidelity), so a few parametrized cases share an id. A
  regression in one of them can be hidden by another that still passes.
- **Toy tasks are only a smoke test.** The five fixtures are small Python bugs
  written for this project. Results that mean something come from the SWE-bench
  tasks above.

## Layout

```
cmd/brokkr/          Go control plane (CLI): brokkr verify, brokkr fix, brokkr bundle
internal/verify/     patch staging, protected-path check, verdict, evidence
internal/agent/      the tool-calling loop and the model's workspace
internal/model/      OpenAI-compatible chat client
internal/bundle/     review bundle (patch + PR description with evidence)
tools/               freetier proxy (Python) and its tests
runner/              Rust sandbox runner: one command, one fresh microVM
guest/brokkr-init    PID 1 inside the guest
scripts/             gates and image build
fixtures/            five small buggy repos with task specs and reference patches
                     (calc also has the adversarial patches used by gate 1)
results/             eval outputs, one directory per run; REPORT.md is generated
                     from them by scripts/make-report.sh
dev/lima.yaml        the reference dev VM
```

## Development

Firecracker needs Linux with KVM. On an Apple M3 or later running macOS 15+, a Lima
VM with nested virtualization provides it:

```bash
limactl start --name brokkr dev/lima.yaml
limactl shell brokkr
scripts/kvm-smoke.sh                     # gate 0; expect PASS
BROKKR_NO_KVM=1 scripts/kvm-smoke.sh     # negative control, expect FAIL
scripts/build-rootfs.sh                  # guest image with brokkr-init
```

Build the runner inside the VM, the control plane on the Mac, then run gate 1:

```bash
# in the VM
(cd runner && CARGO_TARGET_DIR=~/.cache/brokkr/target cargo build --release)
# on the Mac
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/brokkr-linux-arm64 ./cmd/brokkr
# in the VM
mkdir -p ~/.cache/brokkr/bin && cp bin/brokkr-linux-arm64 ~/.cache/brokkr/bin/brokkr
scripts/gate1.sh
```

The reference machine is an Apple M4 (16 GiB) running this Lima VM. Every published
number names the machine it was measured on. Timings here go through two layers of
virtualization, so they are slower than bare metal; comparisons are only made between
runs on the same machine. A bare-metal Linux run is planned and will be reported
separately, not mixed in.

## License

Apache-2.0. See [LICENSE](LICENSE).
