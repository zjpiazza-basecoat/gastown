package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateSchedulerStallFlagsReadyWorkWithFreeCapacity(t *testing.T) {
	sh := &SchedulerHealth{
		QueuedReady:        2,
		Capacity:           polecatCapacitySnapshot{Free: 5, Max: 5},
		LastDispatchAt:     "2026-06-26T20:26:31Z",
		LastDispatchAgeSec: int((6 * time.Minute).Seconds()),
	}

	evaluateSchedulerStall(sh, 5*time.Minute)

	if !sh.Stalled {
		t.Fatal("expected scheduler to be marked stalled")
	}
	if !strings.Contains(sh.StallReason, "ready queued work") {
		t.Fatalf("stall reason = %q, want ready queued work context", sh.StallReason)
	}
}

func TestEvaluateSchedulerStallIgnoresPausedOrFullScheduler(t *testing.T) {
	cases := []SchedulerHealth{
		{Paused: true, QueuedReady: 2, Capacity: polecatCapacitySnapshot{Free: 5, Max: 5}, LastDispatchAgeSec: int((10 * time.Minute).Seconds())},
		{QueuedReady: 2, Capacity: polecatCapacitySnapshot{Free: 0, Max: 5}, LastDispatchAgeSec: int((10 * time.Minute).Seconds())},
		{QueuedReady: 0, Capacity: polecatCapacitySnapshot{Free: 5, Max: 5}, LastDispatchAgeSec: int((10 * time.Minute).Seconds())},
	}

	for i := range cases {
		sh := cases[i]
		evaluateSchedulerStall(&sh, 5*time.Minute)
		if sh.Stalled {
			t.Fatalf("case %d unexpectedly stalled: %s", i, sh.StallReason)
		}
	}
}
