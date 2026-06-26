package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
)

var (
	agentBootRole    string
	agentBootSession string
	agentBootWorkdir string
	agentBootCommand string
	agentBootAttach  bool
)

var agentCmd = &cobra.Command{
	Use:    "agent",
	Hidden: true,
	Short:  "Internal agent runtime helpers",
	RunE:   requireSubcommand,
}

var agentBootCmd = &cobra.Command{
	Use:   "boot",
	Short: "Boot a tmux-backed agent session inside a pod or sandbox",
	Long: `Boot a tmux-backed agent session inside a Kubernetes pod or Agent Sandbox.

This command is intended for in-cluster entrypoints. It ensures a tmux server
and named session exist, starts the configured coding harness once, and then
leaves the container alive for remote 'gt ... attach' clients.`,
	RunE: runAgentBoot,
}

func init() {
	agentBootCmd.Flags().StringVar(&agentBootRole, "role", "polecat", "Agent role name for environment/defaults")
	agentBootCmd.Flags().StringVar(&agentBootSession, "session", "agent", "tmux session name to create")
	agentBootCmd.Flags().StringVar(&agentBootWorkdir, "workdir", "/workspace", "working directory for the agent session")
	agentBootCmd.Flags().StringVar(&agentBootCommand, "command", "", "command to run inside tmux (default: GT_AGENT_COMMAND or claude)")
	agentBootCmd.Flags().BoolVar(&agentBootAttach, "attach", false, "attach after booting instead of waiting forever")
	agentCmd.AddCommand(agentBootCmd)
	rootCmd.AddCommand(agentCmd)
}

func runAgentBoot(cmd *cobra.Command, args []string) error {
	if agentBootCommand == "" {
		agentBootCommand = os.Getenv("GT_AGENT_COMMAND")
	}
	if agentBootCommand == "" {
		agentBootCommand = "claude"
	}
	if agentBootSession == "" {
		return fmt.Errorf("--session is required")
	}
	if agentBootWorkdir == "" {
		agentBootWorkdir = "/workspace"
	}
	if err := os.MkdirAll(agentBootWorkdir, 0755); err != nil {
		return fmt.Errorf("creating workdir: %w", err)
	}

	t := tmux.NewTmux()
	exists, err := t.HasSession(agentBootSession)
	if err != nil {
		return fmt.Errorf("checking tmux session: %w", err)
	}
	if !exists {
		startup := buildAgentBootCommand(agentBootCommand)
		if err := t.NewSessionWithCommand(agentBootSession, agentBootWorkdir, startup); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}
		fmt.Fprintf(os.Stderr, "%s booted %s session %q in %s\n", style.Success.Render("✓"), agentBootRole, agentBootSession, agentBootWorkdir)
	}

	if agentBootAttach {
		return attachToTmuxSession(agentBootSession)
	}
	select {}
}

func buildAgentBootCommand(agentCommand string) string {
	parts := []string{
		"[ ! -f AGENTS.md ] || gt prime || true",
		"gt mail check --inject || true",
		agentCommand,
	}
	return strings.Join(parts, " && ")
}
