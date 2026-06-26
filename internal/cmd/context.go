package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/gtcontext"
	"github.com/steveyegge/gastown/internal/style"
)

var (
	contextType        string
	contextURL         string
	contextToken       string
	contextNamespace   string
	contextKubeContext string
	contextTownRoot    string
)

var contextCmd = &cobra.Command{
	Use:     "context",
	Aliases: []string{"ctx"},
	Short:   "Manage local CLI contexts for local or in-cluster Towns",
	Long: `Manage the local gt client context.

A remote context makes this gt binary act as a thin client for an in-cluster
Gas Town. Commands such as 'gt mayor attach' and 'gt polecat attach' connect
to the configured Town gateway instead of requiring a local ~/gt workspace.`,
	RunE: requireSubcommand,
}

var contextAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or update a context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kind, err := gtcontext.NormalizeType(contextType)
		if err != nil {
			return err
		}
		if kind == gtcontext.TypeRemote && contextURL == "" {
			return fmt.Errorf("--url is required for remote contexts")
		}
		cfg, err := gtcontext.Load()
		if err != nil {
			return err
		}
		cfg.Contexts[args[0]] = gtcontext.Context{
			Type:        kind,
			URL:         contextURL,
			Token:       contextToken,
			Namespace:   contextNamespace,
			KubeContext: contextKubeContext,
			TownRoot:    contextTownRoot,
		}
		if cfg.Current == "" {
			cfg.Current = args[0]
		}
		if err := gtcontext.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("%s context %q saved\n", style.Success.Render("✓"), args[0])
		return nil
	},
}

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Select the current context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := gtcontext.Load()
		if err != nil {
			return err
		}
		if args[0] != "local" {
			if _, ok := cfg.Contexts[args[0]]; !ok {
				return fmt.Errorf("unknown context %q", args[0])
			}
		}
		cfg.Current = args[0]
		if err := gtcontext.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("%s using context %q\n", style.Success.Render("✓"), args[0])
		return nil
	},
}

var contextCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the current context",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, ctx, _, err := gtcontext.CurrentContext()
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s", name, ctx.Type)
		if ctx.URL != "" {
			fmt.Printf("\t%s", ctx.URL)
		}
		fmt.Println()
		return nil
	},
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List contexts",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := gtcontext.Load()
		if err != nil {
			return err
		}
		current := cfg.Current
		if current == "" {
			current = "local"
		}
		fmt.Fprintf(os.Stdout, "%-2s %-20s %-12s %s\n", "", "NAME", "TYPE", "TARGET")
		mark := ""
		if current == "local" {
			mark = "*"
		}
		fmt.Fprintf(os.Stdout, "%-2s %-20s %-12s %s\n", mark, "local", "local", "current directory / ~/gt")
		for _, name := range cfg.Names() {
			ctx := cfg.Contexts[name]
			mark = ""
			if name == current {
				mark = "*"
			}
			target := ctx.URL
			if target == "" {
				target = ctx.Namespace
			}
			fmt.Fprintf(os.Stdout, "%-2s %-20s %-12s %s\n", mark, name, ctx.Type, target)
		}
		return nil
	},
}

func init() {
	contextAddCmd.Flags().StringVar(&contextType, "type", "remote", "Context type: local, remote, or kubernetes")
	contextAddCmd.Flags().StringVar(&contextURL, "url", "", "Remote Town gateway URL (remote contexts)")
	contextAddCmd.Flags().StringVar(&contextToken, "token", "", "Bearer token for the remote Town gateway")
	contextAddCmd.Flags().StringVar(&contextNamespace, "namespace", "", "Kubernetes namespace (kubernetes contexts)")
	contextAddCmd.Flags().StringVar(&contextKubeContext, "kube-context", "", "Kubeconfig context name (kubernetes contexts)")
	contextAddCmd.Flags().StringVar(&contextTownRoot, "town-root", "", "Local town root (local contexts)")

	contextCmd.AddCommand(contextAddCmd)
	contextCmd.AddCommand(contextUseCmd)
	contextCmd.AddCommand(contextCurrentCmd)
	contextCmd.AddCommand(contextListCmd)
	rootCmd.AddCommand(contextCmd)
}
