package test

import (
	"testing"

	"github.com/onexstack/cobrax"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// createTestCommand creates a simple test command tree for testing.
func createTestCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "testcli",
		Short: "Test CLI",
	}

	get := &cobra.Command{
		Use:   "get [resource]",
		Short: "Get a resource",
		Run:   func(_ *cobra.Command, _ []string) {},
	}
	get.Flags().String("output", "json", "Output format")
	get.Flags().Bool("verbose", false, "Verbose output")

	list := &cobra.Command{
		Use:   "list",
		Short: "List resources",
		Run:   func(_ *cobra.Command, _ []string) {},
	}
	list.Flags().Int("limit", 10, "Limit results")

	root.AddCommand(get, list)
	root.AddCommand(cobrax.Command(nil))

	return root
}

func TestGetTools(t *testing.T) {
	cmd := createTestCommand()

	// Test successful tool generation
	tools := GetTools(t, cmd)

	// Should have two tools: get and list
	assert.Len(t, tools, 2, "Expected 2 tools")

	// Verify ToolNames helper
	ToolNames(t, tools, "testcli_get", "testcli_list")

	// Verify each tool has required properties
	for _, tool := range tools {
		assert.NotEmpty(t, tool.Name, "Tool should have a name")
		assert.NotEmpty(t, tool.Description, "Tool should have a description")
		assert.True(t, len(tool.RawInputSchema) > 0 || tool.InputSchema.Type != "", "Tool should have an input schema")

		// Test GetInputSchema
		schema := GetInputSchema(t, tool)
		assert.NotNil(t, schema, "Should return a schema")
		assert.Equal(t, "object", schema.Type, "Schema type should be object")
	}
}

func TestCmdNamesToToolNames(t *testing.T) {
	cmdNames := []string{"get", "list all", "create this item"}
	expectedToolNames := []string{"get", "list_all", "create_this_item"}

	toolNames := CmdPathsToToolNames(cmdNames)
	assert.Equal(t, expectedToolNames, toolNames, "Tool names should match expected format")
}

// createCustomNameCommand creates a test command tree where the cobrax command
// is named "agent" instead of the default "mcp". This mirrors the devenv use
// case where "mcp" is already taken by a business service.
func createCustomNameCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "myapp",
		Short: "Test CLI with custom cobrax command name",
	}

	// A service called "mcp" — the reason we need to rename cobrax.
	mcpService := &cobra.Command{
		Use:   "mcp",
		Short: "MCP business service",
	}
	mcpInstall := &cobra.Command{
		Use:   "install",
		Short: "Install the mcp service",
		Run:   func(_ *cobra.Command, _ []string) {},
	}
	mcpService.AddCommand(mcpInstall)

	status := &cobra.Command{
		Use:   "status",
		Short: "Show status",
		Run:   func(_ *cobra.Command, _ []string) {},
	}

	root.AddCommand(mcpService, status)
	root.AddCommand(cobrax.Command(&cobrax.Config{CommandName: "agent"}))

	return root
}

func TestCustomCommandName(t *testing.T) {
	cmd := createCustomNameCommand()

	// Verify the cobrax command is named "agent" in the tree.
	var agentFound, mcpFound bool
	for _, sub := range cmd.Commands() {
		switch sub.Name() {
		case "agent":
			agentFound = true
		case "mcp":
			mcpFound = true
		}
	}
	assert.True(t, agentFound, "expected 'agent' subcommand from cobrax")
	assert.True(t, mcpFound, "expected 'mcp' subcommand (business service)")

	// Get the tool list via the renamed command.
	tools := GetToolsForCommand(t, cmd, "agent")

	// Should expose mcp_install and status — NOT cobrax internals.
	ToolNames(t, tools, "myapp_mcp_install", "myapp_status")

	// Explicitly verify no cobrax subcommands leaked into the tool list.
	for _, tool := range tools {
		assert.NotContains(t, tool.Name, "agent", "cobrax subcommand %q should not be exposed as a tool", tool.Name)
		assert.NotContains(t, tool.Name, "start", "cobrax 'start' should not be exposed as a tool")
		assert.NotContains(t, tool.Name, "claude", "cobrax 'claude' should not be exposed as a tool")
		assert.NotContains(t, tool.Name, "cursor", "cobrax 'cursor' should not be exposed as a tool")
		assert.NotContains(t, tool.Name, "vscode", "cobrax 'vscode' should not be exposed as a tool")
	}
}
