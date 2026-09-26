#!/usr/bin/env bash
# Agent eval: run `brokkr fix` K times on every task in fixtures/ and record the
# verified verdicts.
#
# Before the agent sees a task, the task itself is checked: the unpatched repo
# must FAIL and the reference patch (patches/good.patch) must PASS. A task that
# fails either check is reported as invalid and excluded, never scored.
#
#   scripts/eval.sh                 # K=3, model from BROKKR_MODEL (default brokkr-qwen2.5-7b-16k)
#   K=5 BROKKR_MODEL=qwen3.5:4b-mlx scripts/eval.sh
#   BROKKR_STRICT_EDITS=1 scripts/eval.sh   # ablation: exact-match edit tool
#   VALIDITY=results/swe/validity.jsonl ... # reuse validate.sh's checks (same image)
#
# Output: results/<stamp>-<model>/{runs.jsonl,summary.md} and per-run evidence
# under ~/.cache/brokkr/runs/. Run inside the Lima VM.
set -uo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
source "$here/scripts/env.sh"
K="${K:-3}"
MODEL="${BROKKR_MODEL:-brokkr-qwen2.5-7b-16k}"
MODE=reindent; [[ "${BROKKR_STRICT_EDITS:-}" == 1 ]] && MODE=strict
stamp="$(date +%Y%m%dT%H%M%S)"
tag="$stamp-${MODEL//[^A-Za-z0-9._-]/_}-$MODE${EVAL_TAG:+-$EVAL_TAG}"
res="$here/results/$tag"
runs="$cache/runs/eval-$tag"
mkdir -p "$res" "$runs"
: >"$res/runs.jsonl"

# TASK_ROOT holds one directory per task (task.json, repo/, patches/good.patch):
# fixtures/ by default, or prepared SWE-bench tasks (scripts/swe/prepare.py).
TASK_ROOT="${TASK_ROOT:-$here/fixtures}"
# TASKS, when set, is run in the order given (a pre-registered order must hold);
# otherwise every task under TASK_ROOT, alphabetically.
tasks=()
if [[ -n "${TASKS:-}" ]]; then
  for t in $TASKS; do
    [[ -d "$TASK_ROOT/$t" ]] && tasks+=("$t") || echo "warning: no task $t under $TASK_ROOT" >&2
  done
else
  for t in "$TASK_ROOT"/*/; do tasks+=("$(basename "$t")"); done
fi

echo "model=$MODEL edits=$MODE K=$K tasks=${#tasks[@]} root=$TASK_ROOT host=$BROKKR_HOST"
for t in "${tasks[@]}"; do
  fx="$TASK_ROOT/$t"
  mkdir -p "$runs/$t"   # run dirs and their .stderr files live here
  if [[ -n "${VALIDITY:-}" ]]; then
    # Validity already established on this image by scripts/swe/validate.sh.
    v=$(jq -r --arg t "$t" 'select(.task==$t) | "\(.baseline) \(.gold) \(.valid)"' "$VALIDITY" | tail -1)
    read -r bv gv ok <<<"${v:-MISSING MISSING false}"
    b="$bv  (from $VALIDITY)"; g="$gv  (from $VALIDITY)"
    [[ "$ok" != true ]] && g="INVALID  (from $VALIDITY)"
  else
    b=$("$brokkr" verify --task "$fx/task.json" --repo "$fx/repo" --out "$runs/$t/check-baseline" 2>/dev/null)
    g=$("$brokkr" verify --task "$fx/task.json" --repo "$fx/repo" --patch "$fx/patches/good.patch" --out "$runs/$t/check-reference" 2>/dev/null)
  fi
  if [[ "${b%%  *}" != FAIL || "${g%%  *}" != PASS ]]; then
    echo "$t: INVALID TASK (baseline=${b%%  *} reference=${g%%  *}); skipped"
    jq -nc --arg task "$t" --arg b "${b%%  *}" --arg g "${g%%  *}" \
      '{task:$task, invalid:true, baseline:$b, reference:$g}' >>"$res/runs.jsonl"
    continue
  fi
  for i in $(seq 1 "$K"); do
    out="$runs/$t/run-$i"
    line=$("$brokkr" fix --task "$fx/task.json" --repo "$fx/repo" --out "$out" --model "$MODEL" 2>"$out.stderr")
    if [[ $? -eq 4 ]]; then
      # Free tier exhausted. Every further call would park too: stop, keep what
      # finished, and say when to resume. The parked run is not scored.
      echo "$line"
      echo "stopping: $(jq -r .parked_until "$out/summary.json") is when the allowance returns"
      jq -c --arg run "$i" '. + {run: ($run|tonumber)}' "$out/summary.json" >>"$res/runs.jsonl"
      parked=1
      break 2
    fi
    if [[ -f "$out/summary.json" ]]; then
      jq -c --arg run "$i" --arg dir "$out" '. + {run: ($run|tonumber), dir: $dir}' "$out/summary.json" >>"$res/runs.jsonl"
    else
      jq -nc --arg task "$t" --arg run "$i" --arg err "$(tail -1 "$out.stderr")" \
        '{task:$task, run:($run|tonumber), verdict:"ERROR", reasons:[$err]}' >>"$res/runs.jsonl"
    fi
    printf "%-10s run %d  %s\n" "$t" "$i" "$line"
  done
done

[[ -n "${parked:-}" ]] && echo "PARTIAL: budget parked before all runs finished" | tee "$res/PARTIAL"
python3 - "$res" "$MODEL" "$K" "$BROKKR_HOST" "$MODE" <<'PY'
import json, sys, collections
res, model, k, host, mode = sys.argv[1:]
rows = [json.loads(l) for l in open(f"{res}/runs.jsonl")]
by = collections.OrderedDict()
for r in rows:
    by.setdefault(r["task"], []).append(r)
out = [f"# Agent eval: {model}, {mode} edits", "", f"Host: {host}  ", f"Runs per task: {k}. Verdicts are Brokkr's re-verification of the final diff, not the agent's claim.",
       "Runs cut short by the model server (timeouts, full context) are infra errors: listed, never scored.", "",
       "| Task | Verified PASS / scored | Agent claimed fixed | Over-claims | Infra errors | Median turns | Median test runs | Refusals |",
       "|---|---|---|---|---|---|---|---|"]
tot = collections.Counter()
med = lambda xs: sorted(xs)[len(xs)//2] if xs else "-"
for t, rs in by.items():
    if rs[0].get("invalid"):
        out.append(f"| {t} | invalid task (baseline {rs[0]['baseline']}, reference {rs[0]['reference']}) | | | | | | |")
        continue
    inf = [r for r in rs if r.get("infra_error") or r.get("verdict") == "ERROR"]
    rs = [r for r in rs if r not in inf]
    p = sum(r.get("verdict") == "PASS" for r in rs)
    c = sum(bool(r.get("agent_claimed_fixed")) for r in rs)
    o = sum(bool(r.get("agent_claimed_fixed")) and r.get("verdict") != "PASS" for r in rs)
    ref = sum(r.get("refusals", 0) for r in rs)
    tot.update(pass_=p, claim=c, over=o, n=len(rs), err=len(inf), ref=ref)
    out.append(f"| {t} | {p}/{len(rs)} | {c}/{len(rs)} | {o} | {len(inf)} | {med([r.get('turns',0) for r in rs])} | {med([r.get('test_runs',0) for r in rs])} | {ref} |")
out += ["", f"**Total: {tot['pass_']}/{tot['n']} verified PASS over scored runs; agent claimed {tot['claim']}; over-claims {tot['over']}; infra errors {tot['err']} (not scored); tool refusals {tot['ref']}.**", "",
        "An over-claim is a run where the agent said it fixed the task and verification disagreed."]
open(f"{res}/summary.md", "w").write("\n".join(out) + "\n")
print("\n".join(out))
PY
echo "results: $res"
