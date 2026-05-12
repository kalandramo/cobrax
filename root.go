package cobrax

import (
	"github.com/onexstack/cobrax/internal/cfgmgr/cmd/claude"
	"github.com/onexstack/cobrax/internal/cfgmgr/cmd/cursor"
	"github.com/onexstack/cobrax/internal/cfgmgr/cmd/vscode"
	"github.com/spf13/cobra"
)

// Command creates MCP server management commands for a Cobra CLI.
// Pass nil for default configuration or provide a Config for customization.
func Command(config *Config) *cobra.Command {
	name := config.commandName()

	var defaultEnv map[string]string
	if config != nil {
		defaultEnv = config.DefaultEnv
	}

	cmd := &cobra.Command{
		Use:   name,
		Short: "MCP server management",
		Long:  `Manage MCP servers for AI assistants and code editors`,
	}

	// Add subcommands
	cmd.AddCommand(
		startCommand(config),
		toolCommand(config),
		streamCommand(config),
		claude.Command(name, defaultEnv),
		vscode.Command(name, defaultEnv),
		cursor.Command(name, defaultEnv),
	)
	return cmd
}
