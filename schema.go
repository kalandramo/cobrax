package cobrax

import "github.com/onexstack/cobrax/internal/schema"

// ToolInput represents the input structure for command tools.
// Do not `omitempty` the Flags field, there may be required flags inside.
//
// Positional arguments are encoded in two complementary ways:
//
//  1. PositionalArgs (map[string]any): the preferred representation when the
//     command has named positional arguments parsed from cmd.Use. Each key is
//     the canonical argument name (e.g. "module", "title") and the value is
//     the string (or []string for variadic) supplied by the caller. The MCP
//     JSON Schema exposes these as individually-described named properties so
//     that an LLM knows exactly what to supply for each position.
//
//  2. Args ([]string): the legacy flat-array representation. Used as a
//     fallback when a command has no named argument tokens in cmd.Use, or when
//     the caller prefers to supply raw positional values directly.
//
// Only one of the two fields needs to be populated for any given call; the
// execution layer checks PositionalArgs first, then falls back to Args.
type ToolInput struct {
	Flags         map[string]any `json:"flags" jsonschema:"Command line flags"`
	PositionalArgs map[string]any `json:"positional_args,omitempty" jsonschema:"Named positional arguments (preferred when the command defines named arg tokens in its Use string)"`
	Args          []string       `json:"args,omitempty" jsonschema:"Positional command line arguments (legacy flat list; use positional_args when available)"`
}

// ToolOutput represents the output structure for command tools.
type ToolOutput struct {
	StdOut   string `json:"stdout,omitempty" jsonschema:"Standard output"`
	StdErr   string `json:"stderr,omitempty" jsonschema:"Standard error"`
	ExitCode int    `json:"exitCode" jsonschema:"Exit code"`
}

var (
	inputSchema  = schema.New[ToolInput]()
	outputSchema = schema.New[ToolOutput]()
)
