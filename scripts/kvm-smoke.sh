#!/usr/bin/env bash
# Gate 0: can this host run a Firecracker microVM at all?
#
# Run inside a Linux host (the Lima dev VM or a metal box). It checks /dev/kvm,
# fetches a pinned Firecracker release and the matching CI kernel and rootfs,
# boots one microVM, and passes only if the guest reaches a login prompt.
#
#   scripts/kvm-smoke.sh            # expect PASS
#   BROKKR_NO_KVM=1 scripts/kvm-smoke.sh   # negative control: expect FAIL
set -euo pipefail

FC_VERSION="${FC_VERSION:-v1.13.1}"
ARCH="$(uname -m)"
WORK="${BROKKR_CACHE:-$HOME/.cache/brokkr}"
KVM="/dev/kvm"
[[ "${BROKKR_NO_KVM:-}" == 1 ]] && KVM="/dev/kvm-missing-on-purpose"

fail() { echo "FAIL: $*" >&2; exit 1; }

[[ "$(uname -s)" == Linux ]] || fail "not Linux ($(uname -s)); run inside the Lima VM"
[[ -c "$KVM" ]] || fail "$KVM not present; nested virtualization is off or unsupported"
[[ -r "$KVM" && -w "$KVM" ]] || fail "$KVM not accessible; add $USER to the kvm group and log in again"
echo "ok: $KVM present and accessible"

mkdir -p "$WORK" && cd "$WORK"

if [[ ! -x "firecracker-$FC_VERSION" ]]; then
  url="https://github.com/firecracker-microvm/firecracker/releases/download/$FC_VERSION/firecracker-$FC_VERSION-$ARCH.tgz"
  echo "fetch: $url"
  curl -fsSL "$url" | tar -xz
  cp "release-$FC_VERSION-$ARCH/firecracker-$FC_VERSION-$ARCH" "firecracker-$FC_VERSION"
fi

# Kernel and rootfs from Firecracker's public CI bucket, as in their getting-started guide.
CI_VERSION="${FC_VERSION%.*}"
S3="https://s3.amazonaws.com/spec.ccfc.min"
latest_key() {  # latest key under a prefix matching a regex, or empty
  curl -fsSL "http://spec.ccfc.min.s3.amazonaws.com/?prefix=firecracker-ci/$CI_VERSION/$ARCH/$1&list-type=2" \
    | grep -oP "(?<=<Key>)(firecracker-ci/$CI_VERSION/$ARCH/$2)(?=</Key>)" | sort -V | tail -1 || true
}
if [[ ! -f vmlinux ]]; then
  key=$(latest_key vmlinux- 'vmlinux-[0-9]+\.[0-9]+\.[0-9]{1,3}')
  [[ -n "$key" ]] || fail "no CI kernel found for $CI_VERSION/$ARCH"
  echo "fetch: $S3/$key"
  curl -fsSL -o vmlinux "$S3/$key"
fi
if [[ ! -f rootfs.ext4 ]]; then
  # The CI bucket ships a squashfs; convert it to ext4 as the upstream guide does.
  key=$(latest_key ubuntu- 'ubuntu-[0-9]+\.[0-9]+\.squashfs')
  [[ -n "$key" ]] || fail "no CI rootfs found for $CI_VERSION/$ARCH"
  echo "fetch: $S3/$key"
  curl -fsSL -o rootfs.squashfs "$S3/$key"
  command -v unsquashfs >/dev/null || fail "unsquashfs missing; apt-get install squashfs-tools"
  sudo rm -rf squashfs-root
  sudo unsquashfs -q rootfs.squashfs >/dev/null
  truncate -s 1G rootfs.ext4
  sudo mkfs.ext4 -q -d squashfs-root -F rootfs.ext4
  sudo rm -rf squashfs-root rootfs.squashfs
fi

cat > vm.json <<EOF
{
  "boot-source": { "kernel_image_path": "$WORK/vmlinux",
                   "boot_args": "console=ttyS0 reboot=k panic=1 pci=off" },
  "drives": [{ "drive_id": "rootfs", "path_on_host": "$WORK/rootfs.ext4",
               "is_root_device": true, "is_read_only": true }],
  "machine-config": { "vcpu_count": 1, "mem_size_mib": 256 }
}
EOF

log="$WORK/boot.log"
sock="$WORK/fc.sock"
rm -f "$sock"
start=$(date +%s%N)
timeout 30 "./firecracker-$FC_VERSION" --api-sock "$sock" --config-file vm.json >"$log" 2>&1 &
pid=$!

for _ in $(seq 1 300); do
  if grep -q "login:" "$log"; then
    ms=$(( ($(date +%s%N) - start) / 1000000 ))
    kill "$pid" 2>/dev/null || true
    echo "PASS: microVM reached login prompt in ${ms}ms (dev-VM timing, not a benchmark)"
    exit 0
  fi
  kill -0 "$pid" 2>/dev/null || break
  sleep 0.1
done
kill "$pid" 2>/dev/null || true
tail -20 "$log" >&2
fail "microVM did not reach a login prompt; full log at $log"
