package verify

import (
	"testing"
	"time"
)

func TestSlotsLimitConcurrency(t *testing.T) {
	dir := t.TempDir()
	r1, _, err := acquireSlot(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	r2, w2, _ := acquireSlot(dir, 2)
	if w2 > 50*time.Millisecond {
		t.Fatalf("second slot waited %v", w2)
	}
	got := make(chan time.Duration)
	go func() {
		r3, w3, _ := acquireSlot(dir, 2)
		r3()
		got <- w3
	}()
	select {
	case <-got:
		t.Fatal("third run started while both slots were held")
	case <-time.After(300 * time.Millisecond):
	}
	r1()
	if w3 := <-got; w3 < 300*time.Millisecond {
		t.Fatalf("third run waited only %v", w3)
	}
	r2()
}

// Negative control: no limit, no waiting.
func TestNoSlotLimit(t *testing.T) {
	for i := 0; i < 5; i++ {
		if _, w, err := acquireSlot(t.TempDir(), 0); err != nil || w != 0 {
			t.Fatalf("waited %v err %v", w, err)
		}
	}
}
