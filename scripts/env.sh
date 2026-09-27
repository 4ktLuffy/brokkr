# Source inside the Lima VM: paths the brokkr binaries expect.
cache="${BROKKR_CACHE:-$HOME/.cache/brokkr}"
export BROKKR_KERNEL="$cache/vmlinux"
export BROKKR_ROOTFS="$cache/brokkr-rootfs.ext4"
export BROKKR_FIRECRACKER="$cache/firecracker-v1.13.1"
export BROKKR_RUNNER="$cache/target/release/brokkr-runner"
# At most this many microVMs at once on this host, across all brokkr processes
# (internal/verify/slots.go). On the M4 Mac under nested virtualization all
# microVMs share about one core; 2 gave the shortest batch (NIGHTLOG). On a
# bare-metal Linux server, raise it toward the core count.
export BROKKR_SANDBOX_SLOTS="${BROKKR_SANDBOX_SLOTS:-2}"
export BROKKR_SLOT_DIR="${BROKKR_SLOT_DIR:-$cache/slots}"
export BROKKR_HOST="${BROKKR_HOST:-Apple M4 16GiB / Lima vz nested virt / $(uname -r)}"
brokkr="${BROKKR_BIN:-$cache/bin/brokkr}"   # BROKKR_BIN picks a pinned build
