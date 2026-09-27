package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Sandbox slots: a host-wide limit on microVMs running at once, shared by every
// brokkr process through lock files. Beyond what the machine can run in
// parallel, more concurrent VMs only make each one slower: on the development
// Mac (nested virtualization) all microVMs together get about one core, and 8
// runs of the same job took 41.5 s at 8 at a time, 32.0 s at 2 (NIGHTLOG).
// A run over the limit waits, and the wait is recorded apart from the VM time.

// acquireSlot blocks until one of n slots under dir is free. n <= 0 means no
// limit. release must be called when the microVM has exited.
func acquireSlot(dir string, n int) (release func(), waited time.Duration, err error) {
	if n <= 0 {
		return func() {}, 0, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("sandbox slots: %w", err)
	}
	start := time.Now()
	for {
		for i := 0; i < n; i++ {
			f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("slot-%d.lock", i)), os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				return nil, 0, fmt.Errorf("sandbox slots: %w", err)
			}
			// flock is released by the kernel if this process dies, so a
			// crashed run can never hold a slot forever.
			if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
				return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, time.Since(start), nil
			}
			f.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
}
