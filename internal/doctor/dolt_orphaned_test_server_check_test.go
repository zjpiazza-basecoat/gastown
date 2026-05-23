package doctor

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/doltserver"
)

func TestDoltOrphanedTestServerCheck_RunAndFix(t *testing.T) {
	prevClassify := classifyDoltListenersForDoctor
	prevTerminate := terminateOrphanedTestServersForDoctor
	t.Cleanup(func() {
		classifyDoltListenersForDoctor = prevClassify
		terminateOrphanedTestServersForDoctor = prevTerminate
	})

	classifyDoltListenersForDoctor = func(string) []doltserver.DoltServerFinding {
		return []doltserver.DoltServerFinding{
			{PID: 10, Port: 3307, Kind: doltserver.DoltServerProduction},
			{PID: 11, Port: 45123, Kind: doltserver.DoltServerOrphanedTest, SafeToTerminate: true, OwnerPath: "/tmp/gt-test/.dolt-data"},
			{PID: 12, Port: 45124, Kind: doltserver.DoltServerUnknown, OwnerPath: "/var/lib/dolt"},
		}
	}

	check := NewDoltOrphanedTestServerCheck()
	result := check.Run(&CheckContext{TownRoot: "/town"})
	if result.Status != StatusWarning {
		t.Fatalf("Status = %v, want warning", result.Status)
	}
	if !strings.Contains(result.Message, "1 orphaned test Dolt server") {
		t.Fatalf("Message = %q, want safe orphan count", result.Message)
	}
	if len(result.Details) != 2 {
		t.Fatalf("Details len = %d, want 2 non-production findings: %v", len(result.Details), result.Details)
	}

	terminated := false
	terminateOrphanedTestServersForDoctor = func(townRoot string) ([]doltserver.DoltServerFinding, error) {
		terminated = true
		if townRoot != "/town" {
			t.Fatalf("townRoot = %q, want /town", townRoot)
		}
		return []doltserver.DoltServerFinding{{PID: 11, Port: 45123}}, nil
	}
	if err := check.Fix(&CheckContext{TownRoot: "/town"}); err != nil {
		t.Fatalf("Fix() error = %v", err)
	}
	if !terminated {
		t.Fatal("expected Fix to call safe termination helper")
	}
}

func TestDoltOrphanedTestServerCheck_OK(t *testing.T) {
	prevClassify := classifyDoltListenersForDoctor
	t.Cleanup(func() { classifyDoltListenersForDoctor = prevClassify })
	classifyDoltListenersForDoctor = func(string) []doltserver.DoltServerFinding {
		return []doltserver.DoltServerFinding{{PID: 10, Port: 3307, Kind: doltserver.DoltServerProduction}}
	}

	result := NewDoltOrphanedTestServerCheck().Run(&CheckContext{TownRoot: "/town"})
	if result.Status != StatusOK {
		t.Fatalf("Status = %v, want OK", result.Status)
	}
}
