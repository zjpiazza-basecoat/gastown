package cmd

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestReaperDatabaseNamesTrimsConfiguredList(t *testing.T) {
	oldDB := reaperDB
	t.Cleanup(func() { reaperDB = oldDB })

	reaperDB = " hq, gastown ,, beads "
	got := reaperDatabaseNames()
	want := []string{"hq", "gastown", "beads"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reaperDatabaseNames() = %#v, want %#v", got, want)
	}
}

func TestReaperScanReportsMissingSchemaDatabases(t *testing.T) {
	data, err := os.ReadFile("reaper.go")
	if err != nil {
		t.Fatalf("read reaper.go: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "results = append(results, reaper.MissingSchemaScanResult(dbName))") {
		t.Fatal("reaper scan should append an explicit missing-schema result instead of omitting the database")
	}
}

func TestWaitBeforeReaperDatabase(t *testing.T) {
	oldDelay := reaperDBDelay
	t.Cleanup(func() { reaperDBDelay = oldDelay })

	reaperDBDelay = "0s"
	if err := waitBeforeReaperDatabase(0); err != nil {
		t.Fatalf("first database wait returned error: %v", err)
	}
	if err := waitBeforeReaperDatabase(1); err != nil {
		t.Fatalf("zero-delay wait returned error: %v", err)
	}

	reaperDBDelay = "not-a-duration"
	if err := waitBeforeReaperDatabase(1); err == nil {
		t.Fatal("invalid delay should return an error")
	}
}
