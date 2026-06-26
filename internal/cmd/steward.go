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
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
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

func init() {
	stewardStartCmd.Flags().StringVar(&stewardInterval, "interval", "1800", "Validation interval in seconds")
	stewardCmd.AddCommand(stewardStartCmd)
	stewardCmd.AddCommand(stewardStopCmd)
	stewardCmd.AddCommand(stewardRestartCmd)
	stewardCmd.AddCommand(stewardStatusCmd)
	stewardCmd.AddCommand(stewardAttachCmd)
	stewardCmd.AddCommand(stewardScanCmd)
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
    /home/d3adb0y/.local/bin/gt mail send mayor/ \
      -s "✅ Gas Town upgrade validated" \
      -m "Town Steward validated a green local upgrade candidate.\n\nGastown: ${gt_sha}\nBeads: ${bd_sha}\n\nTests/builds passed in isolated workspace: $work\n\nApply when convenient with: /gt-upgrade\nOr CLI: /home/d3adb0y/gt/scripts/gt-upgrade-local.sh" || true
    log "notified mayor of green upgrade ${gt_sha:0:8}/${bd_sha:0:8}"
  else
    log "green candidate already announced"
  fi
}

log "Town Steward started (interval=${INTERVAL}s)"
while true; do
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
2. Periodically run: gt steward scan and gt steward validate-upgrade.
3. Use scan findings to classify: config/steering vs operational cleanup vs platform gap.
4. If validation is green, ensure Mayor/user has mail saying /gt-upgrade is available.
5. If validation is red, file or escalate only when actionable.
6. Never apply upgrades automatically. The human chooses when to run /gt-upgrade.
7. Prefer mail-first, non-invasive notifications. Do not spam duplicate alerts.`,
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
	Rig      string `json:"rig"`
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason"`
	Reusable bool   `json:"reusable"`
}

func runStewardScan(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	report := stewardScanReport{}
	metric := stewardHealthMetric{Timestamp: time.Now().UTC(), FindingsBySeverity: map[string]int{}, FindingsByKind: map[string]int{}}

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
