package cobrax

import "github.com/onexstack/cobrax/internal/schema"

// ToolInput represents the input structure for command tools.
// Do not `omitempty` the Flags field, there may be required flags inside.
//
// Positional arguments are encoded in Args (map[string]any) when the command
// has named positional argument tokens parsed from cmd.Use. Each key is the
// canonical argument name (e.g. "module", "title") and the value is the string
// (or []string for variadic) supplied by the caller. The MCP JSON Schema
// exposes these as individually-described named properties so that an LLM
// knows exactly what to supply for each position.
//
// When a command has no positional argument tokens in cmd.Use, the Args field
// is absent from both the JSON Schema and the decoded input.
type ToolInput struct {
	Flags map[string]any `json:"flags" jsonschema:"Command line flags"`
	Args  map[string]any `json:"args,omitempty" jsonschema:"Positional command line arguments"`
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
