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
	script := stewardScriptPath(townRoot)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("steward loop script not found at %s: %w", script, err)
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
