#!/usr/bin/env bash
# Run Codestral on every valid task of a GitHub-PR task set that has no scored
# row yet, in the pre-registered order, in 3 shards. The same command starts a
# run and resumes one after a park. Run on the Mac; it drives the Lima VM.
#
#   scripts/gh/run-codestral.sh pydantic TAG
set -uo pipefail
cd "$(dirname "$0")/../.."
set_name=${1:?usage: run-codestral.sh SET TAG}; tag=${2:?usage: run-codestral.sh SET TAG}
dir=results/$set_name
until ! limactl shell brokkr bash -lc "ps -eo args | grep -F 'EVAL_TAG=$set_name-codestral' | grep -vqF grep"; do sleep 30; done
python3 - "$dir" "$set_name" "$tag" <<'PY'
import json, glob, sys
d, name, tag = sys.argv[1:]
order = json.load(open(f"{d}/plan.json"))["order_list"]
valid = {json.loads(l)["task"] for l in open(f"{d}/validity.jsonl") if json.loads(l)["valid"]}
scored = set()
for f in glob.glob(f"results/*{name}-codestral-*/runs.jsonl"):
    for l in open(f):
        r = json.loads(l)
        if r.get("verdict") != "ERROR" and not r.get("infra_error"):
            scored.add(r["task"])
todo = [t for t in order if t in valid and t not in scored]
for k in range(3):
    open(f"bin/{name}-{tag}-s{k}.txt", "w").write(" ".join(todo[k::3]))
print(f"{name}: {len(todo)} tasks to run")
PY
for k in 0 1 2; do
  tasks=$(cat bin/$set_name-$tag-s$k.txt); [[ -z "$tasks" ]] && continue
  limactl shell brokkr bash -lc "cd \"$PWD\" && source scripts/env.sh && BROKKR_BIN=\$HOME/.cache/brokkr/bin/brokkr-v0.4 VALIDITY=$dir/validity.jsonl TASK_ROOT=\$HOME/.cache/brokkr-gh/tasks TASKS=\"$tasks\" K=1 EVAL_TAG=$set_name-codestral-$tag-s$k BROKKR_MODEL=codestral-latest BROKKR_MODEL_URL=http://host.lima.internal:11502/v1 BROKKR_CONTEXT_TOKENS=250000 BROKKR_MAX_TURNS=40 BROKKR_MAX_SANDBOX_RUNS=10 BROKKR_COMPACT_ABOVE=16000 scripts/eval.sh > /tmp/$set_name-$tag-s$k.log 2>&1" &
  sleep 3
done
wait
echo "$set_name $tag finished at $(date +%H:%M)"
