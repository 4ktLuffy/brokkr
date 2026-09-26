# Source inside the Lima VM: paths the brokkr binaries expect.
cache="${BROKKR_CACHE:-$HOME/.cache/brokkr}"
export BROKKR_KERNEL="$cache/vmlinux"
export BROKKR_ROOTFS="$cache/brokkr-rootfs.ext4"
export BROKKR_FIRECRACKER="$cache/firecracker-v1.13.1"
export BROKKR_RUNNER="$cache/target/release/brokkr-runner"
export BROKKR_HOST="${BROKKR_HOST:-Apple M4 16GiB / Lima vz nested virt / $(uname -r)}"
brokkr="${BROKKR_BIN:-$cache/bin/brokkr}"   # BROKKR_BIN picks a pinned build
