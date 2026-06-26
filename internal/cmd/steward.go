package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
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

var stewardInterval string

func init() {
	stewardStartCmd.Flags().StringVar(&stewardInterval, "interval", "1800", "Validation interval in seconds")
	stewardCmd.AddCommand(stewardStartCmd)
	stewardCmd.AddCommand(stewardStopCmd)
	stewardCmd.AddCommand(stewardRestartCmd)
	stewardCmd.AddCommand(stewardStatusCmd)
	stewardCmd.AddCommand(stewardAttachCmd)
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
	script, err := ensureStewardScript(townRoot)
	if err != nil {
		return err
	}

	start := exec.Command("tmux", "new-session", "-d", "-s", stewardSessionName(), "-c", townRoot, script)
	start.Env = append(os.Environ(), "GT_TOWN_ROOT="+townRoot, "GT_STEWARD_INTERVAL="+stewardInterval)
	if err := start.Run(); err != nil {
		return fmt.Errorf("starting Town Steward: %w", err)
	}
	fmt.Printf("%s Town Steward started. Attach with: %s\n", style.Bold.Render("✓"), style.Dim.Render("gt steward attach"))
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
