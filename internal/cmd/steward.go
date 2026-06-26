package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/health"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

var stewardCmd = &cobra.Command{
	Use:     "steward",
	Aliases: []string{"stew"},
	GroupID: GroupAgents,
	Short:   "Manage the Town Steward upgrade/reconciliation controller",
	Long: `Manage the Town Steward - the town-level reconciliation controller.

The Steward validates local Gas Town stack upgrades in an isolated workspace.
It does not apply upgrades automatically. When a candidate passes tests/builds,
it notifies the Mayor/user that /gt-upgrade is safe to run when convenient.

Role shortcut: "steward" resolves to the hq-steward session.`,
	RunE: requireSubcommand,
}

var stewardStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Town Steward controller",
	RunE:  runStewardStart,
}

var stewardStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Town Steward controller",
	RunE:  runStewardStop,
}

var stewardRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Town Steward controller",
	RunE:  runStewardRestart,
}

var stewardStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Town Steward status",
	RunE:  runStewardStatus,
}

var stewardAttachCmd = &cobra.Command{
	Use:     "attach",
	Aliases: []string{"at"},
	Short:   "Attach to the Town Steward session",
	RunE:    runStewardAttach,
}

var stewardScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan for systemic Gas Town issues and config drift",
	Long: `Scan town state for symptoms the Steward should reconcile: scheduler pressure,
raw polecat directory caps, recovery/MQ backlog, dead agents, Dolt health, and dirty
local core repos. The scan is read-only and emits findings for the Steward agent to
classify before notifying or patching.`,
	RunE: runStewardScan,
}

var stewardReconcileHealthCmd = &cobra.Command{
	Use:   "reconcile-health",
	Short: "Safely reconcile gt health findings",
	Long: `Safely reconcile gt health findings with production guardrails.

Actions:
  - terminate Dolt sql-server listeners that are NOT the production port/PID
  - run gt dolt cleanup without --force for safe orphan DB cleanup
  - rerun gt health --json for verification

This command deliberately refuses destructive or ambiguous repair.`,
	RunE: runStewardReconcileHealth,
}

var stewardReconcilePolecatsCmd = &cobra.Command{
	Use:   "reconcile-polecats",
	Short: "Safely reconcile recovery-blocked polecats",
	Long: `Safely reconcile recovery-blocked polecats.

Actions:
  - inspect idle polecats in NEEDS_MQ_SUBMIT/NEEDS_RECOVERY states
  - refresh stale cleanup state with gt polecat check-recovery --reconcile-cleanup
  - submit clean, named work branches to the merge queue with --no-cleanup
  - refuse ambiguous cases such as running sessions, HEAD branches, dirty worktrees,
    stashes, or branches whose issue cannot be inferred by gt mq submit
  - rerun scheduler status after reconciliation

This command is intentionally conservative. It only nukes polecats after
check-recovery reports SAFE_TO_NUKE and gt polecat nuke safety checks pass. It
does not force-push, reset, stash, or bypass nuke safety checks.`,
	RunE: runStewardReconcilePolecats,
}

var stewardProposalCmd = &cobra.Command{
	Use:     "proposal",
	Aliases: []string{"proposals"},
	Short:   "Manage Steward approval proposals",
	RunE:    requireSubcommand,
}

var stewardProposalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Steward proposals",
	RunE:  runStewardProposalList,
}

var stewardProposalCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Steward proposal",
	RunE:  runStewardProposalCreate,
}

var stewardProposalApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Approve and execute a Steward proposal",
	Args:  cobra.ExactArgs(1),
	RunE:  runStewardProposalApprove,
}

var stewardProposalRejectCmd = &cobra.Command{
	Use:   "reject <id>",
	Short: "Reject a Steward proposal",
	Args:  cobra.ExactArgs(1),
	RunE:  runStewardProposalReject,
}

var stewardProposalProcessApprovedCmd = &cobra.Command{
	Use:   "process-approved",
	Short: "Execute approved Steward proposals and mark them completed",
	RunE:  runStewardProposalProcessApproved,
}

var stewardValidateUpgradeCmd = &cobra.Command{
	Use:   "validate-upgrade",
	Short: "Validate local Gas Town upgrade candidate once",
	Long: `Validate local Gastown/beads upgrade candidates in an isolated workspace.

This command is deterministic support tooling for the Steward agent. It does not
apply upgrades. On a green candidate, it sends Mayor mail telling the human that
/gt-upgrade is available to run when convenient.`,
	RunE: runStewardValidateUpgrade,
}

var stewardInterval string
var stewardReconcilePolecatsRig string
var stewardReconcilePolecatsDryRun bool
var stewardReconcilePolecatsLimit int
var stewardProposalJSON bool
var stewardProposalPending bool
var stewardProposalKind string
var stewardProposalTitle string
var stewardProposalSummary string
var stewardProposalDetails string
var stewardProposalApproveCommand string
var stewardProposalRejectCommand string
var stewardProposalRisk string

func init() {
	stewardStartCmd.Flags().StringVar(&stewardInterval, "interval", "1800", "Validation interval in seconds")
	stewardReconcilePolecatsCmd.Flags().StringVar(&stewardReconcilePolecatsRig, "rig", "app", "Rig to reconcile")
	stewardReconcilePolecatsCmd.Flags().BoolVar(&stewardReconcilePolecatsDryRun, "dry-run", false, "Print planned actions without changing state")
	stewardReconcilePolecatsCmd.Flags().IntVar(&stewardReconcilePolecatsLimit, "limit", 0, "Maximum number of polecats to reconcile (0 = no limit)")
	stewardCmd.AddCommand(stewardStartCmd)
	stewardCmd.AddCommand(stewardStopCmd)
	stewardCmd.AddCommand(stewardRestartCmd)
	stewardCmd.AddCommand(stewardStatusCmd)
	stewardCmd.AddCommand(stewardAttachCmd)
	stewardCmd.AddCommand(stewardScanCmd)
	stewardCmd.AddCommand(stewardReconcileHealthCmd)
	stewardCmd.AddCommand(stewardReconcilePolecatsCmd)
	stewardProposalListCmd.Flags().BoolVar(&stewardProposalJSON, "json", false, "Output JSON")
	stewardProposalListCmd.Flags().BoolVar(&stewardProposalPending, "pending", false, "Only pending proposals")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalKind, "kind", "implementation", "Proposal kind (implementation, upgrade)")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalTitle, "title", "", "Proposal title")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalSummary, "summary", "", "Proposal summary")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalDetails, "details", "", "Proposal details")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalRisk, "risk", "", "Risk note")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalApproveCommand, "approve-command", "", "Command to run on approval")
	stewardProposalCreateCmd.Flags().StringVar(&stewardProposalRejectCommand, "reject-command", "", "Command to run on rejection")
	_ = stewardProposalCreateCmd.MarkFlagRequired("title")
	stewardProposalCmd.AddCommand(stewardProposalListCmd, stewardProposalCreateCmd, stewardProposalApproveCmd, stewardProposalRejectCmd, stewardProposalProcessApprovedCmd)
	stewardCmd.AddCommand(stewardProposalCmd)
	stewardCmd.AddCommand(stewardValidateUpgradeCmd)
	rootCmd.AddCommand(stewardCmd)
}

func stewardSessionName() string { return session.StewardSessionName() }

func stewardScriptPath(townRoot string) string {
	return filepath.Join(townRoot, "scripts", "gt-steward-loop.sh")
}

const stewardLoopScript = `#!/usr/bin/env bash
set -euo pipefail

TOWN_ROOT="${GT_TOWN_ROOT:-$HOME/gt}"
HALL="${GT_GASTOWNHALL:-$HOME/code/gastownhall}"
RUNTIME="$TOWN_ROOT/.runtime/steward"
INTERVAL="${GT_STEWARD_INTERVAL:-1800}"
STATE="$RUNTIME/state"
LOG="$RUNTIME/steward.log"
mkdir -p "$RUNTIME" "$STATE"
exec 9>"$RUNTIME/loop.lock"
if ! flock -n 9; then
  printf '[%s] Town Steward loop already running; exiting duplicate\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  exit 0
fi

log() { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

clone_or_update() {
  local src="$1" dst="$2" name="$3"
  if [ ! -d "$dst/.git" ]; then
    rm -rf "$dst"
    git clone --quiet "$src" "$dst"
  fi
  git -C "$dst" fetch --all --prune --quiet
  git -C "$dst" checkout --quiet main
  git -C "$dst" reset --hard --quiet origin/main
  log "$name candidate $(git -C "$dst" rev-parse --short HEAD)"
}

health_has_findings() {
  local file="$1"
  python3 - "$file" <<'PY'
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
except Exception:
    sys.exit(0)
server = data.get("server") or {}
processes = data.get("processes") or {}
if server.get("running") is False:
    sys.exit(0)
if int(processes.get("zombie_count") or 0) > 0:
    sys.exit(0)
if data.get("orphans"):
    sys.exit(0)
sys.exit(1)
PY
}

create_unhealthy_proposal_once() {
  local details
  details=$(python3 - "$STATE/health-after.json" <<'PY'
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
except Exception as exc:
    print(f"Could not parse health JSON: {exc}")
    sys.exit(0)
print(json.dumps(data, indent=2)[:6000])
PY
)
  /home/d3adb0y/.local/bin/gt steward proposal create \
    --kind health \
    --title "Gas Town health still unhealthy after safe reconciliation" \
    --summary "Steward safe health reconciliation ran, but gt health still reports findings that require manual review." \
    --details "$details" \
    --risk "Approval runs gt dolt cleanup --force, which removes orphan databases that safe cleanup refused." \
    --approve-command "gt dolt cleanup --force && gt steward reconcile-health" || true
}

process_approved_proposals_once() {
  log "processing approved proposals"
  timeout 180 /home/d3adb0y/.local/bin/gt steward proposal process-approved >>"$LOG" 2>&1 || log "proposal processing timed out/failed; continuing"
}

reconcile_health_once() {
  log "health scan"
  timeout 120 /home/d3adb0y/.local/bin/gt steward scan >>"$LOG" 2>&1 || log "steward scan timed out/failed; continuing"
  if ! timeout 30 /home/d3adb0y/.local/bin/gt health --json >"$STATE/health-before.json" 2>>"$LOG"; then
    log "gt health unavailable; skipping automatic health reconciliation"
    return 0
  fi
  if health_has_findings "$STATE/health-before.json"; then
    log "gt health findings detected; running safe reconciliation"
    timeout 90 /home/d3adb0y/.local/bin/gt steward reconcile-health >>"$LOG" 2>&1 || log "reconcile-health timed out/failed; continuing"
    timeout 30 /home/d3adb0y/.local/bin/gt health --json >"$STATE/health-after.json" 2>>"$LOG" || true
    if health_has_findings "$STATE/health-after.json"; then
      log "gt health remains unhealthy after safe reconciliation; creating/deduping proposal"
      create_unhealthy_proposal_once
    else
      log "gt health healthy after reconciliation"
    fi
  else
    log "gt health healthy"
  fi
}

reconcile_polecats_once() {
  log "polecat recovery reconciliation"
  timeout 300 /home/d3adb0y/.local/bin/gt steward reconcile-polecats --rig app >>"$LOG" 2>&1 || log "reconcile-polecats timed out/failed; continuing"
}

validate_once() {
  local work="$RUNTIME/validation"
  mkdir -p "$work"
  clone_or_update "$HALL/gastown" "$work/gastown" gastown
  clone_or_update "$HALL/beads" "$work/beads" beads

  local gt_sha bd_sha key
  gt_sha=$(git -C "$work/gastown" rev-parse HEAD)
  bd_sha=$(git -C "$work/beads" rev-parse HEAD)
  key="$gt_sha:$bd_sha"

  log "validating upgrade candidate ${gt_sha:0:8}/${bd_sha:0:8}"
  (
    cd "$work/gastown"
    go test ./internal/git
    go test ./internal/cmd -run 'TestApplyAgentFieldsToCapacitySnapshot|TestPolecat|TestScheduler|TestAgent'
    go build ./cmd/gt
  )
  (
    cd "$work/beads"
    go test ./cmd/bd
    go build ./cmd/bd
  )

  if [ "$(cat "$STATE/last_green" 2>/dev/null || true)" != "$key" ]; then
    printf '%s' "$key" > "$STATE/last_green"
    /home/d3adb0y/.local/bin/gt steward proposal create \
      --kind upgrade \
      --title "Gas Town upgrade ready" \
      --summary "Town Steward validated a green local upgrade candidate." \
      --details "Gastown: ${gt_sha}\nBeads: ${bd_sha}\n\nTests/builds passed in isolated workspace: $work" \
      --risk "Applies local gt/bd binaries and may require session reload/restart." \
      --approve-command "/home/d3adb0y/gt/scripts/gt-upgrade-local.sh" || true
    log "created upgrade-ready proposal ${gt_sha:0:8}/${bd_sha:0:8}"
  else
    log "green candidate already announced"
  fi
}

log "Town Steward started (interval=${INTERVAL}s)"
while true; do
  process_approved_proposals_once >>"$LOG" 2>&1 || true
  reconcile_health_once >>"$LOG" 2>&1 || true
  reconcile_polecats_once >>"$LOG" 2>&1 || true
  if validate_once >>"$LOG" 2>&1; then
    log "validation pass complete"
  else
    status=$?
    log "validation failed with status $status; not announcing upgrade"
    date -u +%Y-%m-%dT%H:%M:%SZ > "$STATE/last_red_at"
  fi
  if [ "${GT_STEWARD_ONCE:-0}" = "1" ]; then
    exit 0
  fi
  sleep "$INTERVAL"
done
`

func ensureStewardScript(townRoot string) (string, error) {
	script := stewardScriptPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(script), 0755); err != nil {
		return "", fmt.Errorf("creating steward script dir: %w", err)
	}
	if err := os.WriteFile(script, []byte(stewardLoopScript), 0755); err != nil {
		return "", fmt.Errorf("writing steward loop script: %w", err)
	}
	return script, nil
}

func stewardIsRunning() bool {
	return exec.Command("tmux", "has-session", "-t", stewardSessionName()).Run() == nil
}

func runStewardStart(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	if stewardIsRunning() {
		return fmt.Errorf("Town Steward already running. Attach with: gt steward attach")
	}
	if _, err := ensureStewardScript(townRoot); err != nil {
		return err
	}
	stewardDir := filepath.Join(townRoot, "steward")
	if err := os.MkdirAll(stewardDir, 0755); err != nil {
		return fmt.Errorf("creating steward dir: %w", err)
	}

	accountsPath := constants.MayorAccountsPath(townRoot)
	runtimeConfigDir, _, _ := config.ResolveAccountConfigDir(accountsPath, "")
	if runtimeConfigDir == "" {
		runtimeConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}

	t := tmux.NewTmux()
	_, err = session.StartSession(t, session.SessionConfig{
		SessionID:        stewardSessionName(),
		WorkDir:          stewardDir,
		Role:             string(session.RoleSteward),
		TownRoot:         townRoot,
		AgentName:        "Steward",
		RuntimeConfigDir: runtimeConfigDir,
		Beacon: session.BeaconConfig{
			Recipient: "steward",
			Sender:    "human",
			Topic:     "reconciliation",
		},
		Instructions: `You are the Town Steward, the autonomous reconciliation controller for Gas Town itself.

Your loop:
1. Run gt prime.
2. Immediately run the deterministic controller script: /home/d3adb0y/gt/scripts/gt-steward-loop.sh
3. Keep that script running; it scans, safely reconciles health, safely reconciles polecat recovery debt, validates upgrades, reports proposals, and sleeps between cycles.
4. If the script exits, inspect the error, fix safe local causes, and restart it.
5. Safe health reconciliation goes through gt steward reconcile-health only.
6. Never restart production Dolt blindly; collect diagnostics first and escalate.
7. Never apply upgrades automatically. The human chooses when to run /gt-upgrade.
8. Prefer mail-first, non-invasive notifications. Do not spam duplicate alerts.`,
		WaitForAgent: true,
		WaitFatal:    true,
		AutoRespawn:  true,
		AcceptBypass: true,
	})
	if err != nil {
		return fmt.Errorf("starting Town Steward agent: %w", err)
	}
	fmt.Printf("%s Town Steward agent started. Attach with: %s\n", style.Bold.Render("✓"), style.Dim.Render("gt steward attach"))
	return nil
}

func runStewardStop(cmd *cobra.Command, args []string) error {
	if !stewardIsRunning() {
		fmt.Println("Town Steward is not running.")
		return nil
	}
	if err := exec.Command("tmux", "kill-session", "-t", stewardSessionName()).Run(); err != nil {
		return fmt.Errorf("stopping Town Steward: %w", err)
	}
	fmt.Println("✓ Town Steward stopped")
	return nil
}

func runStewardRestart(cmd *cobra.Command, args []string) error {
	_ = runStewardStop(cmd, args)
	return runStewardStart(cmd, args)
}

func runStewardStatus(cmd *cobra.Command, args []string) error {
	if stewardIsRunning() {
		fmt.Printf("%s Town Steward is running (%s)\n", style.Bold.Render("✓"), stewardSessionName())
	} else {
		fmt.Printf("%s Town Steward is not running\n", style.Bold.Render("○"))
	}
	return nil
}

func runStewardAttach(cmd *cobra.Command, args []string) error {
	if !stewardIsRunning() {
		return fmt.Errorf("Town Steward is not running. Start with: gt steward start")
	}
	attach := exec.Command("tmux", "attach-session", "-t", stewardSessionName())
	attach.Stdin = os.Stdin
	attach.Stdout = os.Stdout
	attach.Stderr = os.Stderr
	return attach.Run()
}

type stewardFinding struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
}

type stewardScanReport struct {
	Findings []stewardFinding `json:"findings"`
}

type stewardHealthMetric struct {
	Timestamp          time.Time               `json:"timestamp"`
	FindingsTotal      int                     `json:"findings_total"`
	FindingsBySeverity map[string]int          `json:"findings_by_severity"`
	FindingsByKind     map[string]int          `json:"findings_by_kind"`
	Scheduler          *stewardSchedulerStatus `json:"scheduler,omitempty"`
	Polecats           map[string]int          `json:"polecats,omitempty"`
	DirtyCoreRepos     []string                `json:"dirty_core_repos,omitempty"`
}

type stewardSchedulerStatus struct {
	QueuedTotal int `json:"queued_total"`
	QueuedReady int `json:"queued_ready"`
	Capacity    struct {
		Max             int `json:"max"`
		Working         int `json:"working"`
		RecoveryBlocked int `json:"recovery_blocked"`
		ReusableIdle    int `json:"reusable_idle"`
		Free            int `json:"free"`
		ActiveSessions  int `json:"active_sessions"`
	} `json:"capacity"`
}

type polecatListItem struct {
	Rig              string `json:"rig"`
	Name             string `json:"name"`
	State            string `json:"state"`
	CleanupStatus    string `json:"cleanup_status"`
	ActiveMR         string `json:"active_mr"`
	Branch           string `json:"branch"`
	Verdict          string `json:"verdict"`
	Reason           string `json:"reason"`
	Reusable         bool   `json:"reusable"`
	SafeToNuke       bool   `json:"safe_to_nuke"`
	NeedsRecovery    bool   `json:"needs_recovery"`
	NeedsMQSubmit    bool   `json:"needs_mq_submit"`
	MQStatus         string `json:"mq_status"`
	SessionRunning   bool   `json:"session_running"`
	CountsToCapacity bool   `json:"counts_toward_capacity"`
}

type stewardGTHealth struct {
	Server struct {
		Running bool  `json:"running"`
		PID     int   `json:"pid"`
		Port    int   `json:"port"`
		Latency int64 `json:"latency_ms"`
	} `json:"server"`
	Processes struct {
		ZombieCount int   `json:"zombie_count"`
		ZombiePIDs  []int `json:"zombie_pids"`
	} `json:"processes"`
	Orphans []struct {
		Name string `json:"name"`
		Size string `json:"size"`
	} `json:"orphans"`
}

func runStewardScan(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	report := stewardScanReport{}
	metric := stewardHealthMetric{Timestamp: time.Now().UTC(), FindingsBySeverity: map[string]int{}, FindingsByKind: map[string]int{}}

	if out, err := runStewardCapture(townRoot, "gt", "health", "--json"); err == nil {
		var h stewardGTHealth
		if json.Unmarshal(out, &h) == nil {
			if !h.Server.Running {
				report.Findings = append(report.Findings, stewardFinding{Severity: "critical", Kind: "dolt-server-down", Summary: "Production Dolt server is not running"})
			}
			if h.Processes.ZombieCount > 0 {
				report.Findings = append(report.Findings, stewardFinding{Severity: "high", Kind: "dolt-zombie-listeners", Summary: "Non-production Dolt sql-server listeners present", Detail: fmt.Sprintf("pids=%v", h.Processes.ZombiePIDs)})
			}
			if len(h.Orphans) > 0 {
				var names []string
				for _, o := range h.Orphans {
					names = append(names, o.Name)
				}
				report.Findings = append(report.Findings, stewardFinding{Severity: "medium", Kind: "orphan-databases", Summary: "Orphan Dolt databases present", Detail: strings.Join(names, ", ")})
			}
		}
	} else {
		report.Findings = append(report.Findings, stewardFinding{Severity: "high", Kind: "gt-health-unavailable", Summary: "Could not read gt health", Detail: err.Error()})
	}

	if out, err := runStewardCapture(townRoot, "gt", "scheduler", "status", "--json"); err == nil {
		var status stewardSchedulerStatus
		if json.Unmarshal(out, &status) == nil {
			metric.Scheduler = &status
			if status.QueuedReady > 0 && status.Capacity.Free > 0 && status.Capacity.ActiveSessions == 0 {
				report.Findings = append(report.Findings, stewardFinding{
					Severity: "high",
					Kind:     "scheduler-dispatch-stalled",
					Summary:  "Scheduler has ready work and free capacity but no active sessions",
					Detail:   fmt.Sprintf("queued_ready=%d free=%d reusable_idle=%d recovery_blocked=%d", status.QueuedReady, status.Capacity.Free, status.Capacity.ReusableIdle, status.Capacity.RecoveryBlocked),
				})
			}
			if status.Capacity.RecoveryBlocked > 0 {
				report.Findings = append(report.Findings, stewardFinding{Severity: "medium", Kind: "recovery-debt", Summary: "Polecat recovery debt present", Detail: fmt.Sprintf("recovery_blocked=%d", status.Capacity.RecoveryBlocked)})
			}
		}
	} else {
		report.Findings = append(report.Findings, stewardFinding{Severity: "medium", Kind: "scheduler-status-unavailable", Summary: "Could not read scheduler status", Detail: err.Error()})
	}

	if out, err := runStewardCapture(townRoot, "gt", "polecat", "list", "app", "--all", "--json"); err == nil {
		var items []polecatListItem
		if json.Unmarshal(out, &items) == nil {
			counts := map[string]int{}
			reusable := 0
			for _, item := range items {
				if item.Reusable {
					reusable++
				}
				key := item.Verdict + ":" + item.Reason
				counts[key]++
			}
			metric.Polecats = counts
			metric.Polecats["total"] = len(items)
			metric.Polecats["reusable"] = reusable
			if len(items) >= 30 && reusable == 0 {
				report.Findings = append(report.Findings, stewardFinding{Severity: "high", Kind: "polecat-raw-dir-cap", Summary: "app rig is at raw polecat directory cap with no reusable dirs", Detail: fmt.Sprintf("dirs=%d reusable=%d", len(items), reusable)})
			}
			var keys []string
			for k := range counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var parts []string
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
			}
			if len(parts) > 0 {
				report.Findings = append(report.Findings, stewardFinding{Severity: "info", Kind: "polecat-workstate-summary", Summary: "app polecat workstate distribution", Detail: strings.Join(parts, ", ")})
			}
		}
	}

	for _, repo := range []struct{ name, path string }{{"gastown", filepath.Join(os.Getenv("HOME"), "code", "gastownhall", "gastown")}, {"beads", filepath.Join(os.Getenv("HOME"), "code", "gastownhall", "beads")}} {
		if out, err := exec.Command("git", "-C", repo.path, "status", "--porcelain").Output(); err == nil && len(bytes.TrimSpace(out)) > 0 {
			metric.DirtyCoreRepos = append(metric.DirtyCoreRepos, repo.name)
			report.Findings = append(report.Findings, stewardFinding{Severity: "medium", Kind: "dirty-core-repo", Summary: repo.name + " core repo has local changes", Detail: string(bytes.TrimSpace(out))})
		}
	}

	recordStewardHealthMetric(townRoot, &metric, report.Findings)
	if len(report.Findings) == 0 {
		fmt.Println("Town Steward scan: no findings")
		return nil
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
	return nil
}

func recordStewardHealthMetric(townRoot string, metric *stewardHealthMetric, findings []stewardFinding) {
	if metric == nil {
		return
	}
	metric.FindingsTotal = len(findings)
	for _, finding := range findings {
		metric.FindingsBySeverity[finding.Severity]++
		metric.FindingsByKind[finding.Kind]++
	}
	runtimeDir := filepath.Join(townRoot, ".runtime", "steward")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return
	}
	data, err := json.Marshal(metric)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(runtimeDir, "health.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func runStewardCapture(townRoot string, name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Dir = townRoot
	return c.Output()
}

func runStewardReconcileHealth(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	state, err := doltserver.LoadState(townRoot)
	if err != nil {
		return fmt.Errorf("loading production Dolt state: %w", err)
	}
	prodPort := state.Port
	prodPID := 0
	if running, pid, err := doltserver.IsRunning(townRoot); err == nil && running {
		prodPID = pid
	}

	zombies := health.FindZombieServers([]int{prodPort})
	for _, pid := range zombies.PIDs {
		if pid == 0 || pid == prodPID {
			fmt.Printf("skip pid %d (production or invalid)\n", pid)
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Printf("skip pid %d: %v\n", pid, err)
			continue
		}
		fmt.Printf("terminating non-production Dolt sql-server pid %d\n", pid)
		_ = proc.Signal(syscall.SIGTERM)
	}

	if zombies.Count > 0 {
		time.Sleep(2 * time.Second)
		remaining := health.FindZombieServers([]int{prodPort})
		for _, pid := range remaining.PIDs {
			if pid == 0 || pid == prodPID {
				continue
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			fmt.Printf("force-killing non-production Dolt sql-server pid %d\n", pid)
			_ = proc.Signal(syscall.SIGKILL)
		}
	}

	fmt.Println("running safe orphan cleanup (no --force)")
	cleanup := exec.Command("gt", "dolt", "cleanup")
	cleanup.Dir = townRoot
	cleanup.Stdout = os.Stdout
	cleanup.Stderr = os.Stderr
	if err := cleanup.Run(); err != nil {
		fmt.Printf("safe orphan cleanup did not fully complete: %v\n", err)
	}

	fmt.Println("verification: gt health --json")
	verify := exec.Command("gt", "health", "--json")
	verify.Dir = townRoot
	verify.Stdout = os.Stdout
	verify.Stderr = os.Stderr
	if err := verify.Run(); err != nil {
		return fmt.Errorf("verifying health: %w", err)
	}
	return nil
}

type stewardReconcilePolecatsReport struct {
	Rig       string                           `json:"rig"`
	DryRun    bool                             `json:"dry_run"`
	Examined  int                              `json:"examined"`
	Changed   int                              `json:"changed"`
	Submitted int                              `json:"submitted"`
	Skipped   int                              `json:"skipped"`
	Items     []stewardReconcilePolecatOutcome `json:"items"`
}

type stewardReconcilePolecatOutcome struct {
	Polecat string `json:"polecat"`
	Verdict string `json:"verdict"`
	Action  string `json:"action"`
	Detail  string `json:"detail,omitempty"`
}

type stewardPolecatGitState struct {
	Clean                 bool     `json:"clean"`
	UncommittedFiles      []string `json:"uncommitted_files"`
	UnpushedCommits       int      `json:"unpushed_commits"`
	UnpreservedPatchCount int      `json:"unpreserved_patch_count"`
	StashCount            int      `json:"stash_count"`
	SharedStashCount      int      `json:"shared_stash_count"`
}

func runStewardReconcilePolecats(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	rigName := strings.TrimSpace(stewardReconcilePolecatsRig)
	if rigName == "" {
		return fmt.Errorf("--rig is required")
	}

	items, err := stewardListPolecats(townRoot, rigName)
	if err != nil {
		return err
	}
	report := stewardReconcilePolecatsReport{Rig: rigName, DryRun: stewardReconcilePolecatsDryRun}
	for _, item := range items {
		if item.Rig != rigName || item.Reusable || (!item.NeedsRecovery && !item.NeedsMQSubmit) {
			continue
		}
		if stewardReconcilePolecatsLimit > 0 && report.Examined >= stewardReconcilePolecatsLimit {
			break
		}
		report.Examined++
		outcome := stewardReconcileOnePolecat(townRoot, item)
		report.Items = append(report.Items, outcome)
		switch outcome.Action {
		case "refreshed", "submitted", "nuked":
			report.Changed++
			if outcome.Action == "submitted" {
				report.Submitted++
			}
		default:
			report.Skipped++
		}
	}

	fmt.Println("verification: gt scheduler status --json")
	verify := exec.Command("gt", "scheduler", "status", "--json")
	verify.Dir = townRoot
	verify.Stdout = os.Stdout
	verify.Stderr = os.Stderr
	if err := verify.Run(); err != nil {
		return fmt.Errorf("verifying scheduler status: %w", err)
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
	return nil
}

func stewardListPolecats(townRoot, rigName string) ([]polecatListItem, error) {
	out, err := runStewardCapture(townRoot, "gt", "polecat", "list", rigName, "--all", "--json")
	if err != nil {
		return nil, fmt.Errorf("listing polecats for %s: %w", rigName, err)
	}
	var items []polecatListItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parsing polecat list: %w", err)
	}
	return items, nil
}

func stewardReconcileOnePolecat(townRoot string, item polecatListItem) stewardReconcilePolecatOutcome {
	addr := item.Rig + "/" + item.Name
	outcome := stewardReconcilePolecatOutcome{Polecat: addr, Verdict: item.Verdict}
	if item.SessionRunning || item.State == "working" {
		outcome.Action = "skipped"
		outcome.Detail = "session is running"
		return outcome
	}

	refreshed, err := stewardCheckRecovery(townRoot, addr, !stewardReconcilePolecatsDryRun)
	if err != nil {
		outcome.Action = "skipped"
		outcome.Detail = "check-recovery failed: " + err.Error()
		return outcome
	}
	if refreshed.Reusable || refreshed.SafeToNuke {
		if stewardReconcilePolecatsDryRun {
			outcome.Action = "dry-run"
			outcome.Detail = fmt.Sprintf("would nuke; check-recovery reports %s (%s)", refreshed.Verdict, refreshed.Reason)
			return outcome
		}
		if err := stewardNukePolecat(townRoot, addr); err != nil {
			outcome.Action = "skipped"
			outcome.Detail = "nuke refused/failed: " + err.Error()
			return outcome
		}
		outcome.Action = "nuked"
		outcome.Detail = fmt.Sprintf("nuked after check-recovery reported %s (%s)", refreshed.Verdict, refreshed.Reason)
		return outcome
	}
	if !refreshed.NeedsRecovery {
		if stewardReconcilePolecatsDryRun {
			outcome.Action = "dry-run"
			outcome.Detail = fmt.Sprintf("would refresh stale state; check-recovery reports %s (%s)", refreshed.Verdict, refreshed.Reason)
			return outcome
		}
		outcome.Action = "refreshed"
		outcome.Detail = fmt.Sprintf("check-recovery now reports %s (%s)", refreshed.Verdict, refreshed.Reason)
		return outcome
	}
	if !refreshed.NeedsMQSubmit {
		outcome.Action = "skipped"
		outcome.Detail = fmt.Sprintf("manual recovery required: %s (%s)", refreshed.Verdict, refreshed.Reason)
		return outcome
	}
	if refreshed.Branch == "" || refreshed.Branch == "HEAD" {
		outcome.Action = "skipped"
		outcome.Detail = "needs MQ submit but branch is ambiguous: " + refreshed.Branch
		return outcome
	}

	gitState, err := stewardPolecatGitStateFor(townRoot, addr)
	if err != nil {
		outcome.Action = "skipped"
		outcome.Detail = "git-state failed: " + err.Error()
		return outcome
	}
	if !gitState.Clean || gitState.StashCount > 0 || len(gitState.UncommittedFiles) > 0 {
		outcome.Action = "skipped"
		outcome.Detail = fmt.Sprintf("dirty worktree: clean=%v uncommitted=%d unpushed=%d stash=%d", gitState.Clean, len(gitState.UncommittedFiles), gitState.UnpushedCommits, gitState.StashCount)
		return outcome
	}

	if stewardReconcilePolecatsDryRun {
		outcome.Action = "dry-run"
		outcome.Detail = "would submit branch " + refreshed.Branch + " to merge queue"
		return outcome
	}

	if err := stewardSubmitPolecatBranch(townRoot, item.Rig, refreshed.Branch); err != nil {
		outcome.Action = "skipped"
		outcome.Detail = "mq submit failed: " + err.Error()
		return outcome
	}
	after, err := stewardCheckRecovery(townRoot, addr, true)
	if err != nil {
		outcome.Action = "submitted"
		outcome.Detail = "submitted branch " + refreshed.Branch + "; post-check failed: " + err.Error()
		return outcome
	}
	outcome.Action = "submitted"
	outcome.Detail = fmt.Sprintf("submitted branch %s; post-check %s (%s)", refreshed.Branch, after.Verdict, after.Reason)
	return outcome
}

func stewardCheckRecovery(townRoot, addr string, reconcileCleanup bool) (*RecoveryStatus, error) {
	args := []string{"polecat", "check-recovery", addr, "--json"}
	if reconcileCleanup {
		args = append(args, "--reconcile-cleanup")
	}
	out, err := runStewardCapture(townRoot, "gt", args...)
	if err != nil {
		return nil, err
	}
	var status RecoveryStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func stewardPolecatGitStateFor(townRoot, addr string) (*stewardPolecatGitState, error) {
	out, err := runStewardCapture(townRoot, "gt", "polecat", "git-state", addr, "--json")
	if err != nil {
		return nil, err
	}
	var state stewardPolecatGitState
	if err := json.Unmarshal(out, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func stewardNukePolecat(townRoot, addr string) error {
	cmd := exec.Command("gt", "polecat", "nuke", addr)
	cmd.Dir = townRoot
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	return nil
}

func stewardSubmitPolecatBranch(townRoot, rigName, branch string) error {
	rigPath := filepath.Join(townRoot, rigName)
	cmd := exec.Command("gt", "mq", "submit", "--branch", branch, "--no-cleanup")
	cmd.Dir = rigPath
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	return nil
}

type stewardProposal struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary,omitempty"`
	Details        string    `json:"details,omitempty"`
	Risk           string    `json:"risk,omitempty"`
	ApproveCommand string    `json:"approve_command,omitempty"`
	RejectCommand  string    `json:"reject_command,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func stewardProposalPath(townRoot string) string {
	return filepath.Join(townRoot, ".runtime", "steward", "proposals.jsonl")
}

func loadStewardProposals(townRoot string) ([]stewardProposal, error) {
	path := stewardProposalPath(townRoot)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []stewardProposal
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var p stewardProposal
		if err := json.Unmarshal(line, &p); err == nil && p.ID != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func saveStewardProposals(townRoot string, proposals []stewardProposal) error {
	path := stewardProposalPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, p := range proposals {
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func ensureStewardProposal(townRoot string, proposal stewardProposal) {
	if proposal.Title == "" || proposal.Kind == "" {
		return
	}
	proposals, err := loadStewardProposals(townRoot)
	if err != nil {
		return
	}
	for _, p := range proposals {
		if p.Status == "pending" && p.Kind == proposal.Kind && p.Title == proposal.Title {
			return
		}
	}
	now := time.Now().UTC()
	proposal.ID = fmt.Sprintf("stew-%d", now.UnixNano())
	proposal.Status = "pending"
	proposal.CreatedAt = now
	proposal.UpdatedAt = now
	proposals = append(proposals, proposal)
	_ = saveStewardProposals(townRoot, proposals)
}

func runStewardProposalList(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	proposals, err := loadStewardProposals(townRoot)
	if err != nil {
		return err
	}
	var filtered []stewardProposal
	for _, p := range proposals {
		if stewardProposalPending && p.Status != "pending" {
			continue
		}
		filtered = append(filtered, p)
	}
	if stewardProposalJSON {
		if filtered == nil {
			filtered = []stewardProposal{}
		}
		data, _ := json.MarshalIndent(filtered, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if len(filtered) == 0 {
		fmt.Println("No Steward proposals")
		return nil
	}
	for _, p := range filtered {
		fmt.Printf("%s [%s/%s] %s\n", p.ID, p.Kind, p.Status, p.Title)
		if p.Summary != "" {
			fmt.Printf("  %s\n", p.Summary)
		}
	}
	return nil
}

func runStewardProposalCreate(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	proposals, err := loadStewardProposals(townRoot)
	if err != nil {
		return err
	}
	// Deduplicate pending proposals by kind+title so Steward loops do not spam modals.
	for _, p := range proposals {
		if p.Status == "pending" && p.Kind == stewardProposalKind && p.Title == stewardProposalTitle {
			fmt.Println(p.ID)
			return nil
		}
	}
	now := time.Now().UTC()
	p := stewardProposal{
		ID:             fmt.Sprintf("stew-%d", now.UnixNano()),
		Kind:           stewardProposalKind,
		Status:         "pending",
		Title:          stewardProposalTitle,
		Summary:        stewardProposalSummary,
		Details:        stewardProposalDetails,
		Risk:           stewardProposalRisk,
		ApproveCommand: stewardProposalApproveCommand,
		RejectCommand:  stewardProposalRejectCommand,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	proposals = append(proposals, p)
	if err := saveStewardProposals(townRoot, proposals); err != nil {
		return err
	}
	fmt.Println(p.ID)
	return nil
}

func runStewardProposalApprove(cmd *cobra.Command, args []string) error {
	return updateStewardProposal(args[0], "approved", true)
}

func runStewardProposalReject(cmd *cobra.Command, args []string) error {
	return updateStewardProposal(args[0], "rejected", false)
}

func updateStewardProposal(id, status string, approve bool) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	proposals, err := loadStewardProposals(townRoot)
	if err != nil {
		return err
	}
	for i := range proposals {
		if proposals[i].ID != id {
			continue
		}
		if proposals[i].Status != "pending" {
			return fmt.Errorf("proposal %s is already %s", id, proposals[i].Status)
		}
		command := proposals[i].RejectCommand
		if approve {
			command = proposals[i].ApproveCommand
		}
		if command != "" {
			run := exec.Command("/bin/sh", "-lc", command)
			run.Dir = townRoot
			run.Stdout = os.Stdout
			run.Stderr = os.Stderr
			if err := run.Run(); err != nil {
				return fmt.Errorf("running proposal command: %w", err)
			}
		}
		proposals[i].Status = status
		proposals[i].UpdatedAt = time.Now().UTC()
		if err := saveStewardProposals(townRoot, proposals); err != nil {
			return err
		}
		fmt.Printf("%s proposal %s\n", status, id)
		return nil
	}
	return fmt.Errorf("proposal %s not found", id)
}

func runStewardProposalProcessApproved(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	proposals, err := loadStewardProposals(townRoot)
	if err != nil {
		return err
	}
	processed := 0
	now := time.Now().UTC()
	for i := range proposals {
		if proposals[i].Status != "approved" {
			continue
		}
		command := strings.TrimSpace(proposals[i].ApproveCommand)
		if command == "" && proposals[i].Kind == "health" && strings.Contains(proposals[i].Title, "health still unhealthy") {
			command = "gt dolt cleanup --force && gt steward reconcile-health"
		}
		if command == "" {
			fmt.Printf("proposal %s approved but has no command; marking completed\n", proposals[i].ID)
			proposals[i].Status = "completed"
			proposals[i].UpdatedAt = now
			processed++
			continue
		}
		fmt.Printf("executing approved proposal %s: %s\n", proposals[i].ID, proposals[i].Title)
		run := exec.Command("/bin/sh", "-lc", command)
		run.Dir = townRoot
		run.Stdout = os.Stdout
		run.Stderr = os.Stderr
		if err := run.Run(); err != nil {
			proposals[i].Status = "failed"
			proposals[i].UpdatedAt = now
			_ = saveStewardProposals(townRoot, proposals)
			return fmt.Errorf("running approved proposal %s: %w", proposals[i].ID, err)
		}
		proposals[i].Status = "completed"
		proposals[i].UpdatedAt = now
		processed++
	}
	if processed == 0 {
		fmt.Println("No approved Steward proposals to process")
	} else if err := saveStewardProposals(townRoot, proposals); err != nil {
		return err
	}
	return nil
}

func runStewardValidateUpgrade(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	script, err := ensureStewardScript(townRoot)
	if err != nil {
		return err
	}
	validate := exec.Command(script)
	validate.Env = append(os.Environ(), "GT_TOWN_ROOT="+townRoot, "GT_STEWARD_ONCE=1")
	validate.Stdout = os.Stdout
	validate.Stderr = os.Stderr
	if err := validate.Run(); err != nil {
		return fmt.Errorf("validating upgrade candidate: %w", err)
	}
	return nil
}
