package cobrax

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/onexstack/cobrax/internal/bridge/flags"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CmdSelector determines if a command should become an MCP tool.
// Return true to include the command as a tool.
// Note: Basic safety filters (hidden, deprecated, non-runnable) are always applied first.
// Commands are tested against selectors in order; the first matching selector wins.
type CmdSelector func(*cobra.Command) bool

// FlagSelector determines if a flag should be included in an MCP tool.
// Return true to include the flag.
// Note: Hidden and deprecated flags are always excluded regardless of this selector.
// This selector is only applied to commands that match the associated CmdSelector.
type FlagSelector func(*pflag.Flag) bool

// MiddlewareFunc is middleware hook that runs after each tool call
// Common uses: error handling, response filtering, metrics collection.
type MiddlewareFunc func(context.Context, mcp.CallToolRequest, ToolInput, ExecuteFunc) (*mcp.CallToolResult, ToolOutput, error)

// ExecuteFunc defines the function signature for executing a tool.
type ExecuteFunc func(context.Context, mcp.CallToolRequest, ToolInput) (*mcp.CallToolResult, ToolOutput, error)

// Selector contains selectors for filtering commands and flags.
// When multiple selectors are configured, they are evaluated in order.
// The first selector whose CmdSelector matches a command is used,
// and its FlagSelector determines which flags are included for that command.
//
// Basic safety filters are always applied automatically:
//   - Hidden/deprecated commands and flags are excluded
//   - Non-runnable commands are excluded
//   - Built-in commands (mcp, help, completion) are excluded
//
// This allows fine-grained control within safe boundaries, such as:
//   - Exposing different flags for different command groups
//   - Applying stricter flag filtering to dangerous commands
//   - Having a default catch-all selector with common flag exclusions
type Selector struct {
	// CmdSelector determines if this selector applies to a command.
	// If nil, accepts all commands that pass basic safety filters.
	// Cannot be used to bypass safety filters (hidden, deprecated, non-runnable).
	CmdSelector CmdSelector

	// LocalFlagSelector determines which flags to include for commands matched by CmdSelector.
	// If nil, includes all flags that pass basic safety filters.
	// Cannot be used to bypass safety filters (hidden, deprecated flags).
	LocalFlagSelector FlagSelector

	// InheritedFlagSelector determines which persistent flags to include for commands matched by CmdSelector.
	// If nil, includes all flags that pass basic safety filters.
	// Cannot be used to bypass safety filters (hidden, deprecated flags).
	InheritedFlagSelector FlagSelector

	// Middleware is an optional middleware hook that wraps around tool execution.
	// Common uses: error handling, response filtering, metrics collection.
	// If nil, no middleware is applied.
	Middleware MiddlewareFunc
}

// enhanceFlagsSchema adds detailed flag information to the flags property.
func (s Selector) enhanceFlagsSchema(schema *jsonschema.Schema, cmd *cobra.Command) {
	// Ensure properties map exists
	if schema.Properties == nil {
		schema.Properties = make(map[string]*jsonschema.Schema)
	}

	// basic filters: skip hidden and deprecated flags, but allow "help" through
	// separately so it can be explicitly added to the schema below.
	filter := func(flag *pflag.Flag) bool {
		if flag.Name == "help" {
			return true // handled explicitly below
		}
		return flag.Hidden || flag.Deprecated != ""
	}

	// Process local flags
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if filter(flag) {
			return
		}

		if s.LocalFlagSelector != nil && !s.LocalFlagSelector(flag) {
			return
		}

		flags.AddFlagToSchema(schema, flag)
	})

	// Process inherited flags
	cmd.InheritedFlags().VisitAll(func(flag *pflag.Flag) {
		// Skip if already added as local flag
		if _, exists := schema.Properties[flag.Name]; exists {
			return
		}

		if filter(flag) {
			return
		}

		if s.InheritedFlagSelector != nil && !s.InheritedFlagSelector(flag) {
			return
		}

		flags.AddFlagToSchema(schema, flag)
	})

	// Explicitly add the help flag (-h/--help) so that MCP clients can request
	// help output for any command. The flag is normally hidden by cobra but is
	// useful to expose in the MCP schema so LLMs can discover command usage.
	schema.Properties["help"] = &jsonschema.Schema{
		Type:        "boolean",
		Description: "Display help information for this command (-h/--help)",
	}

	// Set AdditionalProperties to false
	// See https://github.com/google/jsonschema-go/issues/13
	schema.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
}

// createToolFromCmd creates an MCP tool from a Cobra command.
// The toolNamePrefix is used to replace the root command name in the tool name.
//
// Positional argument handling:
//   - cmd.Use is parsed for named argument tokens (e.g. <module> [title...]).
//   - Each token becomes a named property in the "args" object of the input
//     schema, with its own description, type, and required status.
//   - When no named tokens are detected the "args" property is removed entirely
//     so the LLM is not confused by an inapplicable field.
func (s Selector) createToolFromCmd(cmd *cobra.Command, toolNamePrefix string) *mcp.Tool {
	schema := inputSchema.Copy()
	s.enhanceFlagsSchema(schema.Properties["flags"], cmd)

	// Parse positional argument specs from cmd.Use and annotations.
	specs := parseArgSpecs(cmd)
	enhanceArgsSchema(schema, cmd, specs)

	// Serialize the jsonschema.Schema to json.RawMessage for use with mark3labs/mcp-go.
	rawSchema, err := json.Marshal(schema)
	if err != nil {
		// Fallback to empty object schema on marshalling error.
		rawSchema = json.RawMessage(`{"type":"object"}`)
	}

	tool := &mcp.Tool{
		Name:           toolName(cmd, toolNamePrefix),
		Description:    toolDescription(cmd),
		RawInputSchema: rawSchema,
	}

	// Apply annotations if present.
	if ann := toolAnnotations(cmd); ann != nil {
		tool.Annotations = *ann
	}

	return tool
}

// enhanceArgsSchema updates the input schema's positional-argument properties.
//
// When specs is non-empty (the command has named argument tokens in cmd.Use):
//   - The "args" property is replaced with a structured object schema whose
//     properties correspond to the individual named arguments.
//
// When specs is empty (the command has no positional arguments):
//   - The "args" property is removed entirely so the LLM is not presented with
//     a field that has no meaning for this command.
func enhanceArgsSchema(schema *jsonschema.Schema, _ *cobra.Command, specs []ArgSpec) {
	if schema.Properties == nil {
		schema.Properties = make(map[string]*jsonschema.Schema)
	}

	if len(specs) > 0 {
		// Replace the generic "args" entry with the structured object schema.
		schema.Properties["args"] = buildArgsSchema(specs)
	} else {
		// No positional args: remove the "args" property entirely.
		delete(schema.Properties, "args")
	}
}

// toolName creates a tool name from the command path.
// The toolNamePrefix replaces the root command name in the path.
// For example, if the command path is "omnistrate-ctl cost by-cell list" and
// toolNamePrefix is "omctl", the result is "omctl_cost_by-cell_list".
func toolName(cmd *cobra.Command, toolNamePrefix string) string {
	path := cmd.CommandPath()

	// Replace the root command name with the prefix.
	// The root command name is the first word in the path.
	if spaceIdx := strings.IndexByte(path, ' '); spaceIdx != -1 {
		rest := path[spaceIdx+1:] // sub-command path without the leading space
		if toolNamePrefix == "" {
			// No prefix: use the sub-command path directly.
			path = rest
		} else {
			path = toolNamePrefix + "_" + rest
		}
	} else {
		// Single command (root command itself).
		path = toolNamePrefix
	}

	return strings.ReplaceAll(path, " ", "_")
}

// toolDescription creates a comprehensive tool description.
func toolDescription(cmd *cobra.Command) string {
	var parts []string

	// Use Long description if available, otherwise Short
	if cmd.Long != "" {
		parts = append(parts, cmd.Long)
	} else if cmd.Short != "" {
		parts = append(parts, cmd.Short)
	} else {
		parts = append(parts, fmt.Sprintf("Execute the %s command", cmd.Name()))
	}

	// Add examples if available
	if cmd.Example != "" {
		parts = append(parts, fmt.Sprintf("Examples:\n%s", cmd.Example))
	}

	return strings.Join(parts, "\n")
}

func (s *Selector) execute(ctx context.Context, request mcp.CallToolRequest, input ToolInput) (_ *mcp.CallToolResult, _ ToolOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	if s.Middleware != nil {
		return s.Middleware(ctx, request, input, execute)
	}

	return execute(ctx, request, input)
}
