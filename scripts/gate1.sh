#!/usr/bin/env bash
# Gate 1: the verifier can go red.
#
# Runs every patch in fixtures/calc/patches (and no patch at all) through
# `brokkr verify` in a real microVM and checks each verdict against the expected
# one. The gate passes only if the good patch is the single PASS and every bad
# one is caught. Run inside the Lima VM after scripts/build-rootfs.sh.
set -uo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
cache="${BROKKR_CACHE:-$HOME/.cache/brokkr}"
export BROKKR_KERNEL="$cache/vmlinux"
export BROKKR_ROOTFS="$cache/brokkr-rootfs.ext4"
export BROKKR_FIRECRACKER="$cache/firecracker-v1.13.1"
export BROKKR_RUNNER="$cache/target/release/brokkr-runner"
export BROKKR_HOST="${BROKKR_HOST:-Apple M4 16GiB / Lima vz nested virt / $(uname -r)}"
brokkr="$cache/bin/brokkr"
fx="$here/fixtures/calc"
runs="$cache/runs/gate1-$(date +%Y%m%dT%H%M%S)"

# case          expected   why
cases=(
  "none         FAIL       the bug reproduces before any patch"
  "good         PASS       the real fix"
  "broken       FAIL       a plausible fix that is wrong for most inputs"
  "tamper       REJECTED   edits the tests instead of the code"
  "early-exit   FAIL       exits 0 before any test runs"
  "forge        FAIL       tries to overwrite the sandbox result and reach the network"
)

bad=0
printf "%-12s %-9s %-9s %s\n" CASE EXPECTED GOT REASON
for c in "${cases[@]}"; do
  read -r name want _ <<<"$c"
  patch=()
  [[ "$name" != none ]] && patch=(--patch "$fx/patches/$name.patch")
  line=$("$brokkr" verify --task "$fx/task.json" --repo "$fx/repo" "${patch[@]}" --out "$runs/$name" 2>/dev/null)
  got="${line%%  *}"
  mark=ok; [[ "$got" == "$want" ]] || { mark=WRONG; bad=$((bad + 1)); }
  printf "%-12s %-9s %-9s %s  [%s]\n" "$name" "$want" "$got" "${line#*  }" "$mark"
done

echo "evidence: $runs/*/evidence.json"
if (( bad )); then echo "GATE 1 FAIL: $bad verdict(s) wrong"; exit 1; fi
echo "GATE 1 PASS: one good patch accepted, every bad patch caught"
