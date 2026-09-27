#!/usr/bin/env bash
# Resume the Codestral h0.6.1 held-out runs (Django + SymPy) on every valid task
# that has no scored row yet, in split order, same binaries and budgets as
# plan.json records. Run on the Mac; it drives the Lima VM. Waits for any
# running Codestral shards to finish first.
set -uo pipefail
cd "$(dirname "$0")/.."
tag=${1:?usage: resume-codestral.sh TAG}
until ! limactl shell brokkr bash -lc 'ps -eo args | grep -E "bin/brokkr-(0.6.1|v0.3) fix" | grep -vqF grep'; do sleep 30; done
python3 - "$tag" <<'PY'
import json, glob, sys
tag = sys.argv[1]
def scored(pat):
    d = set()
    for f in glob.glob(pat):
        for l in open(f):
            r = json.loads(l)
            if r.get("verdict") != "ERROR" and not r.get("infra_error"):
                d.add(r["task"])
    return d
valid = lambda vf: {json.loads(l)["task"] for l in open(vf) if json.loads(l)["valid"]}
dj = [t for t in json.load(open("results/swe/split.json"))["test"]
      if t in valid("results/swe/validity.jsonl") and t not in scored("results/*codestral-latest-reindent-swe-heldout-h061*/runs.jsonl")]
sy = [t for t in json.load(open("results/swe/split-sympy.json"))["test"]
      if t in valid("results/swe/validity-sympy.jsonl") and t not in scored("results/*codestral-latest-reindent-swe-sympy-heldout-n2-h061*/runs.jsonl")]
for k in range(3): open(f"bin/{tag}-dj-s{k}.txt", "w").write(" ".join(dj[k::3]))
for k in range(3): open(f"bin/{tag}-sy-s{k}.txt", "w").write(" ".join(sy[k::3]))
print(f"to run: django {len(dj)}, sympy {len(sy)}")
PY
run() { bin=$1; v=$2; t=$3; tasks=$4; [[ -z "$tasks" ]] && return 0
  limactl shell brokkr bash -lc "cd \"$PWD\" && source scripts/env.sh && BROKKR_BIN=\$HOME/.cache/brokkr/bin/$bin VALIDITY=$v TASK_ROOT=\$HOME/.cache/brokkr-swe/tasks TASKS=\"$tasks\" K=1 EVAL_TAG=$t BROKKR_MODEL=codestral-latest BROKKR_MODEL_URL=http://host.lima.internal:11502/v1 BROKKR_CONTEXT_TOKENS=250000 BROKKR_MAX_TURNS=40 BROKKR_MAX_SANDBOX_RUNS=10 BROKKR_COMPACT_ABOVE=16000 scripts/eval.sh > /tmp/$t.log 2>&1"; }
for k in 0 1 2; do run brokkr-0.6.1 results/swe/validity.jsonl swe-heldout-h061-$tag-s$k "$(cat bin/$tag-dj-s$k.txt)" & sleep 3; done
for k in 0 1 2; do run brokkr-v0.3 results/swe/validity-sympy.jsonl swe-sympy-heldout-n2-h061-$tag-s$k "$(cat bin/$tag-sy-s$k.txt)" & sleep 3; done
wait
echo "resume $tag finished at $(date +%H:%M)"
