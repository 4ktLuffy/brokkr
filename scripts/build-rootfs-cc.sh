#!/usr/bin/env bash
# Build the C/C++ guest image: Brokkr's guest root image (scripts/build-rootfs.sh)
# plus a compiler toolchain, for tasks from C and C++ repositories (fmt, jq,
# redis, valkey, micropython). Run inside the Lima VM; needs sudo for the loop
# mount and network for apt. The image stays read-only in every microVM; its
# sha256 is recorded in each run's evidence like the default image's.
set -euo pipefail

cache="${BROKKR_CACHE:-$HOME/.cache/brokkr}"
src="$cache/brokkr-rootfs.ext4"
dst="$cache/brokkr-rootfs-cc.ext4"
# build-essential: gcc, g++, make. cmake: fmt. tcl: redis/valkey's test runner.
# autoconf/automake/libtool/bison/flex: jq's autotools build.
pkgs="build-essential cmake tcl autoconf automake libtool pkg-config bison flex python3"

[[ -f "$src" ]] || { echo "missing $src; run scripts/build-rootfs.sh first" >&2; exit 1; }

cp --sparse=always "$src" "$dst.tmp"
truncate -s 4G "$dst.tmp"
e2fsck -fy "$dst.tmp" >/dev/null 2>&1 || true
resize2fs "$dst.tmp" >/dev/null 2>&1

mnt="$(mktemp -d)"
cleanup() {
  for m in tmp dev/pts dev proc; do sudo umount "$mnt/$m" 2>/dev/null || true; done
  sudo umount "$mnt" 2>/dev/null || true
  rmdir "$mnt" 2>/dev/null || true
}
trap cleanup EXIT
sudo mount -o loop "$dst.tmp" "$mnt"
sudo mount -t proc proc "$mnt/proc"
sudo mount --bind /dev "$mnt/dev"
sudo mount --bind /dev/pts "$mnt/dev/pts"
# The image has no writable /tmp (the guest mounts a tmpfs at boot); apt needs one.
sudo mkdir -p "$mnt/tmp"
sudo mount -t tmpfs -o mode=1777 tmpfs "$mnt/tmp"
# apt needs name resolution during the build only; the guest never has a NIC.
resolv="$mnt/etc/resolv.conf"
saved="$(mktemp)"
sudo cp -P "$resolv" "$saved" 2>/dev/null || true
sudo rm -f "$resolv"
sudo cp /etc/resolv.conf "$resolv" 2>/dev/null || sudo cp -L /etc/resolv.conf "$resolv"
# No terminal input: a question dpkg still asks must fail, not hang (the first
# build sat 52 minutes at a conffile prompt).
sudo chroot "$mnt" /usr/bin/env DEBIAN_FRONTEND=noninteractive bash -c \
  "mkdir -p /var/cache/apt/archives/partial /var/lib/apt/lists/partial /var/log/apt && apt-get update -qq && apt-get install -y -qq --no-install-recommends -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold $pkgs && apt-get clean && rm -rf /var/lib/apt/lists/*" </dev/null >"$cache/rootfs-cc-apt.log" 2>&1 \
  || { echo "FAIL: apt in the image; see $cache/rootfs-cc-apt.log" >&2; tail -20 "$cache/rootfs-cc-apt.log" >&2; exit 1; }
sudo rm -f "$resolv"
sudo cp -P "$saved" "$resolv" 2>/dev/null || true
rm -f "$saved"
versions="$(sudo chroot "$mnt" bash -c 'gcc --version | head -1; cmake --version | head -1; echo "puts [info patchlevel]" | tclsh; autoconf --version | head -1')"
cmp -s "$mnt/usr/sbin/brokkr-init" "$(dirname "$0")/../guest/brokkr-init" \
  || { echo "FAIL: brokkr-init in the image differs from guest/brokkr-init" >&2; exit 1; }
cleanup
trap - EXIT
e2fsck -fy "$dst.tmp" >/dev/null 2>&1 || true
mv "$dst.tmp" "$dst"
sha256sum "$dst" | cut -d' ' -f1 >"$dst.sha256"
echo "built $dst ($(cat "$dst.sha256"))"
echo "$versions"
