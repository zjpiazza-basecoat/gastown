package stewardhealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordDedupesWithinBucket(t *testing.T) {
	town := t.TempDir()
	snap := Snapshot{Timestamp: time.Date(2026, 6, 27, 10, 5, 0, 0, time.UTC), FindingsTotal: 1, FindingsByKind: map[string]int{"recovery-debt": 1}, FindingsBySeverity: map[string]int{"medium": 1}, RecoveryBlocked: 6}
	ok, err := Record(town, snap, time.Hour)
	if err != nil || !ok {
		t.Fatalf("first Record ok=%v err=%v", ok, err)
	}
	snap.Timestamp = snap.Timestamp.Add(10 * time.Minute)
	ok, err = Record(town, snap, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("duplicate unchanged snapshot in same bucket should be skipped")
	}
	entries, err := Load(LedgerPath(town))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
}

func TestImportRuntimeBackfillsAndDedupes(t *testing.T) {
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(RuntimePath(town)), 0755); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-06-27T10:05:00Z","findings_total":1,"findings_by_severity":{"medium":1},"findings_by_kind":{"recovery-debt":1},"scheduler":{"queued_ready":0,"capacity":{"recovery_blocked":6,"active_sessions":0}}}` + "\n"
	if err := os.WriteFile(RuntimePath(town), []byte(line+line), 0644); err != nil {
		t.Fatal(err)
	}
	imported, skipped, err := ImportRuntime(town, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if imported != 1 || skipped != 1 {
		t.Fatalf("imported/skipped=%d/%d want 1/1", imported, skipped)
	}
}

func TestBuildReportShowsImprovement(t *testing.T) {
	town := t.TempDir()
	base := time.Now().UTC().Add(-2 * time.Hour)
	_, err := Record(town, Snapshot{Timestamp: base, FindingsTotal: 3, FindingsByKind: map[string]int{"recovery-debt": 1}, FindingsBySeverity: map[string]int{"medium": 1}, RecoveryBlocked: 6}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Record(town, Snapshot{Timestamp: base.Add(time.Hour), FindingsTotal: 1, FindingsByKind: map[string]int{"recovery-debt": 1}, FindingsBySeverity: map[string]int{"medium": 1}, RecoveryBlocked: 2}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := BuildReport(town, base.Add(-time.Minute), 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RecoveryDebt.First != 6 || rep.RecoveryDebt.Last != 2 || rep.RecoveryDebt.Delta != -4 {
		t.Fatalf("bad recovery trend: %+v", rep.RecoveryDebt)
	}
	out := FormatReport(rep)
	if !strings.Contains(out, "Recovery debt: 6 → 2 (delta -4") {
		t.Fatalf("report missing trend: %s", out)
	}
}
