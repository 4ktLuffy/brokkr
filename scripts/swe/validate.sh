#!/usr/bin/env bash
# Validity check for prepared SWE-bench tasks. For each task: the unpatched repo
# (with the hidden test patch) must FAIL, and SWE-bench's gold patch must PASS.
# Each run's log is also parsed by SWE-bench's own parser and compared with
# Brokkr's (scripts/swe/crosscheck_parser.py). Only tasks that pass all three
# checks are used for agents.
#
#   scripts/swe/validate.sh [instance_id ...]      # default: all prepared tasks
#
# Output: results/swe/validity.jsonl, one line per task. Run inside the Lima VM.
set -uo pipefail
here="$(cd "$(dirname "$0")/../.." && pwd)"
source "$here/scripts/env.sh"
tasks_dir="${BROKKR_SWE_CACHE:-$HOME/.cache/brokkr-swe}/tasks"
out="${VALIDITY_OUT:-$here/results/swe/validity.jsonl}"   # one file per repo keeps readers of the others safe
runs="$cache/runs/swe-validity"
mkdir -p "$runs" "$(dirname "$out")"
ids=("$@")
[[ ${#ids[@]} -eq 0 ]] && ids=($(ls "$tasks_dir"))

check() {
  id=$1; t="$tasks_dir/$id"; r="$runs/$id"; rm -rf "$r"
  "$brokkr" verify --task "$t/task.json" --repo "$t/repo" --out "$r/baseline" >/dev/null 2>&1
  "$brokkr" verify --task "$t/task.json" --repo "$t/repo" --patch "$t/patches/good.patch" --out "$r/gold" >/dev/null 2>&1
  b=$(jq -r .verdict "$r/baseline/evidence.json" 2>/dev/null || echo MISSING)
  g=$(jq -r .verdict "$r/gold/evidence.json" 2>/dev/null || echo MISSING)
  x=$(python3 "$here/scripts/swe/crosscheck_parser.py" "$r/baseline" "$r/gold" >/dev/null 2>&1 && echo SAME || echo DIFFERENT)
  valid=false; [[ $b == FAIL && $g == PASS && $x == SAME ]] && valid=true
  gm=$(jq -c '.tests.missing_required[0:3]' "$r/gold/evidence.json" 2>/dev/null || echo null)
  gs=$(jq -r '.sandbox.guest.run_ms // 0' "$r/gold/evidence.json" 2>/dev/null || echo 0)
  jq -nc --arg id "$id" --arg b "$b" --arg g "$g" --arg x "$x" --argjson v $valid --argjson gm "$gm" --argjson ms "$gs" \
    '{task:$id, baseline:$b, gold:$g, parser_crosscheck:$x, valid:$v, gold_missing_sample:$gm, gold_run_ms:$ms}'
}
export -f check; export tasks_dir runs here brokkr BROKKR_KERNEL BROKKR_ROOTFS BROKKR_FIRECRACKER BROKKR_RUNNER BROKKR_HOST
printf "%s\n" "${ids[@]}" | xargs -P "${JOBS:-3}" -I{} bash -c 'check {}' >>"$out"
jq -s 'group_by(.valid) | map({valid: .[0].valid, n: length})' "$out"
