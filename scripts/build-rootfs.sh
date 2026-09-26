#!/usr/bin/env bash
# Build Brokkr's guest root image: the Firecracker CI Ubuntu rootfs plus
# guest/brokkr-init as PID 1 and a /work mount point. Run inside the Lima VM
# after scripts/kvm-smoke.sh.
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
cache="${BROKKR_CACHE:-$HOME/.cache/brokkr}"
src="$cache/rootfs.ext4"
dst="$cache/brokkr-rootfs.ext4"

[[ -f "$src" ]] || { echo "missing $src; run scripts/kvm-smoke.sh first" >&2; exit 1; }

cp --sparse=always "$src" "$dst.tmp"
# debugfs splits its -R argument on spaces and always exits 0, so stage the file
# at a space-free path and check the result explicitly. /sbin is a symlink to
# usr/sbin in this image; write through the real directory.
stage="$(mktemp)"
cp "$here/guest/brokkr-init" "$stage"
debugfs -w -R "write $stage /usr/sbin/brokkr-init" "$dst.tmp" >/dev/null 2>&1
rm -f "$stage"
debugfs -w -R "mkdir /work" "$dst.tmp" >/dev/null 2>&1
debugfs -w -R "mkdir /opt" "$dst.tmp" >/dev/null 2>&1   # absent in the CI image
debugfs -w -R "mkdir /opt/env" "$dst.tmp" >/dev/null 2>&1
for attr in "mode 0100755" "uid 0" "gid 0"; do
  debugfs -w -R "sif /usr/sbin/brokkr-init $attr" "$dst.tmp" >/dev/null 2>&1
done
debugfs -R "cat /usr/sbin/brokkr-init" "$dst.tmp" 2>/dev/null | cmp -s - "$here/guest/brokkr-init" \
  || { echo "FAIL: brokkr-init did not land in the image" >&2; rm -f "$dst.tmp"; exit 1; }
for d in /work /opt/env; do
  debugfs -R "stat $d" "$dst.tmp" 2>/dev/null | grep -q "Type: directory" \
    || { echo "FAIL: $d mount point missing from the image" >&2; rm -f "$dst.tmp"; exit 1; }
done
mv "$dst.tmp" "$dst"
sha256sum "$dst" | cut -d' ' -f1 >"$dst.sha256"
echo "built $dst ($(cat "$dst.sha256"))"
