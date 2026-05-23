package doctor

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/doltserver"
)

var (
	classifyDoltListenersForDoctor        = doltserver.ClassifyDoltListeners
	terminateOrphanedTestServersForDoctor = doltserver.TerminateOrphanedTestDoltServers
)

// DoltOrphanedTestServerCheck detects random-port Dolt listeners left behind by tests.
type DoltOrphanedTestServerCheck struct {
	FixableCheck
	findings []doltserver.DoltServerFinding
}

// NewDoltOrphanedTestServerCheck creates a Dolt test-server process check.
func NewDoltOrphanedTestServerCheck() *DoltOrphanedTestServerCheck {
	return &DoltOrphanedTestServerCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "dolt-orphaned-test-servers",
				CheckDescription: "Detect orphaned random-port Dolt test servers",
				CheckCategory:    CategoryCleanup,
			},
		},
	}
}

// Run checks for orphaned random-port Dolt test servers.
func (c *DoltOrphanedTestServerCheck) Run(ctx *CheckContext) *CheckResult {
	c.findings = classifyDoltListenersForDoctor(ctx.TownRoot)
	var reportable []doltserver.DoltServerFinding
	for _, f := range c.findings {
		if f.Kind == doltserver.DoltServerProduction {
			continue
		}
		reportable = append(reportable, f)
	}

	if len(reportable) == 0 {
		return &CheckResult{
			Name:     c.Name(),
			Status:   StatusOK,
			Message:  "No extra Dolt test server listeners",
			Category: c.CheckCategory,
		}
	}

	details := make([]string, 0, len(reportable))
	safeCount := 0
	for _, f := range reportable {
		if f.SafeToTerminate {
			safeCount++
		}
		detail := fmt.Sprintf("PID %d port %d: %s", f.PID, f.Port, f.Kind)
		if f.OwnerPath != "" {
			detail += fmt.Sprintf(" owner=%s", f.OwnerPath)
		}
		if f.Reason != "" {
			detail += fmt.Sprintf(" (%s)", f.Reason)
		}
		details = append(details, detail)
	}

	message := fmt.Sprintf("%d non-production Dolt listener(s) detected", len(reportable))
	fixHint := "Inspect listed Dolt listeners before manual cleanup"
	if safeCount > 0 {
		message = fmt.Sprintf("%d orphaned test Dolt server(s) can be cleaned safely", safeCount)
		fixHint = "Run 'gt doctor --fix' to clean safe orphaned test Dolt servers"
	}

	return &CheckResult{
		Name:     c.Name(),
		Status:   StatusWarning,
		Message:  message,
		Details:  details,
		FixHint:  fixHint,
		Category: c.CheckCategory,
	}
}

// Fix stops only safe, temp-owned orphan test Dolt servers.
func (c *DoltOrphanedTestServerCheck) Fix(ctx *CheckContext) error {
	_, err := terminateOrphanedTestServersForDoctor(ctx.TownRoot)
	return err
}
