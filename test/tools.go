package test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
)

// GetToolsForCommand runs `<commandName> tools` and returns the parsed list of tools.
// commandName is the Use name of the cobrax command in the Cobra tree (e.g. "mcp" or "agent").
func GetToolsForCommand(t *testing.T, cmd *cobra.Command, commandName string) []*mcp.Tool {
	cmd.SetArgs([]string{commandName, "tools"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Failed to generate tools: %v", err)
	}

	// Verify the mcp-tools.json file was created and contains valid JSON
	data, err := os.ReadFile("mcp-tools.json")
	if err != nil {
		t.Fatalf("Failed to read mcp-tools.json: %v", err)
	}

	var tools []*mcp.Tool
	if err := json.Unmarshal(data, &tools); err != nil {
		t.Fatalf("Failed to unmarshal mcp-tools.json: %v", err)
	}

	// Clean up the generated file
	if err := os.Remove("mcp-tools.json"); err != nil {
		t.Logf("Warning: failed to remove mcp-tools.json: %v", err)
	}

	return tools
}

// GetTools runs `mcp tools` command and returns the parsed list of tools.
// It assumes the cobrax command uses the default name "mcp".
// For custom command names, use GetToolsForCommand.
func GetTools(t *testing.T, cmd *cobra.Command) []*mcp.Tool {
	return GetToolsForCommand(t, cmd, "mcp")
}

// GetInputSchema extracts and returns the input schema from a tool.
// It reads from RawInputSchema when available, falling back to InputSchema.
func GetInputSchema(t *testing.T, tool *mcp.Tool) *jsonschema.Schema {
	// Prefer RawInputSchema (set by cobrax when using mark3labs/mcp-go).
	if len(tool.RawInputSchema) > 0 {
		schema := &jsonschema.Schema{}
		if err := json.Unmarshal(tool.RawInputSchema, schema); err != nil {
			t.Fatalf("Tool %q: failed to unmarshal RawInputSchema: %v", tool.Name, err)
		}
		return schema
	}

	// Fall back to marshalling the structured InputSchema.
	data, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("Tool %q: failed to marshal InputSchema: %v", tool.Name, err)
	}

	schema := &jsonschema.Schema{}
	if err := json.Unmarshal(data, schema); err != nil {
		t.Fatalf("Tool %q: failed to unmarshal InputSchema into *jsonschema.Schema: %v", tool.Name, err)
	}

	return schema
}

// ToolNames checks that the provided tools match the expectedNames
func ToolNames(t *testing.T, tools []*mcp.Tool, expectedNames ...string) {
	if len(tools) != len(expectedNames) {
		t.Errorf("expected %v tools, got %v", len(expectedNames), len(tools))
	}

	for _, tool := range tools {
		if !slices.Contains(expectedNames, tool.Name) {
			t.Errorf("Tool name not expected: %q", tool.Name)
		}
	}
}

// CmdPathsToToolNames converts command names with spaces to tool names with underscores
func CmdPathsToToolNames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, strings.ReplaceAll(path, " ", "_"))
	}

	return names
}

// ToolsForCommand runs `<commandName> tools` and checks the output tool names against expectedNames.
// commandName is the Use name of the cobrax command in the Cobra tree.
func ToolsForCommand(t *testing.T, cmd *cobra.Command, commandName string, expectedNames ...string) {
	tools := GetToolsForCommand(t, cmd, commandName)

	t.Run("Expected Tools", func(t *testing.T) {
		ToolNames(t, tools, expectedNames...)
	})

	t.Run("Valid JSON Schema", func(t *testing.T) {
		// Check that each tool's input schema is valid JSON Schema
		for _, tool := range tools {
			if len(tool.RawInputSchema) > 0 || tool.InputSchema.Type != "" {
				schema := GetInputSchema(t, tool)
				if schema.Type != "object" {
					t.Errorf("Tool %q: expected schema type 'object', got %q", tool.Name, schema.Type)
				}

				// In the flat schema format, "flags" and "args" are no longer
				// nested sub-objects — all parameters appear as direct top-level
				// properties of the schema object.  Verify that neither legacy
				// wrapper key is present.
				if _, ok := schema.Properties["flags"]; ok {
					t.Errorf("Tool %q: flat schema must not contain a nested 'flags' property", tool.Name)
				}
				if _, ok := schema.Properties["args"]; ok {
					t.Errorf("Tool %q: flat schema must not contain a nested 'args' property", tool.Name)
				}
			}
		}
	})
}

// Tools runs `mcp tools` and checks the output tool names against expectedNames.
// It assumes the cobrax command uses the default name "mcp".
// For custom command names, use ToolsForCommand.
func Tools(t *testing.T, cmd *cobra.Command, expectedNames ...string) {
	ToolsForCommand(t, cmd, "mcp", expectedNames...)
}
