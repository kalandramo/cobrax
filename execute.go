package cobrax

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// executablePath holds the absolute path to the running binary. It is resolved
// once at startup and used by the subprocess-based [execute] function to
// re-invoke the same binary when serving tools through the legacy [Config] API.
var executablePath = initExecPath()

// initExecPath resolves the path of the currently running executable.
// It panics on error because a missing executable path means the process
// cannot re-invoke itself, which is a fatal misconfiguration.
func initExecPath() string {
	path, err := os.Executable()
	if err != nil {
		panic(fmt.Sprintf("failed to get executable path: %v", err))
	}

	return path
}

// execute runs the CLI tool by re-invoking the current binary as a subprocess.
//
// This is the execution model used by the original [Config]-based API. The MCP
// server process re-launches itself with the decoded command arguments, captures
// stdout/stderr, and returns them in a [ToolOutput].
//
// Non-zero exit codes are surfaced in ToolOutput.ExitCode rather than as Go
// errors. A Go error is only returned if the subprocess cannot be launched at
// all (e.g. the binary is not found or lacks execute permission).
func execute(ctx context.Context, request mcp.CallToolRequest, input ToolInput) (*mcp.CallToolResult, ToolOutput, error) {
	name := request.Params.Name
	slog.Info("subprocess MCP tool request received", "tool", name)

	// Build the argument slice from the tool name and input.
	args := buildCommandArgs(name, input)
	slog.Debug("executing subprocess command",
		"tool", name,
		"input", input,
		"args", args,
	)

	// Launch the subprocess and capture its output.
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, executablePath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exitCode := 0

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// The process ran but exited with a non-zero status; surface the
			// exit code in the output rather than as a Go error.
			exitCode = exitErr.ExitCode()
		} else {
			// A non-exit error means the subprocess could not start at all.
			slog.Error("subprocess failed to launch", "tool", name, "error", err)
			return nil, ToolOutput{}, err
		}
	}

	return nil, ToolOutput{
		StdOut:   stdout.String(),
		StdErr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// splitToolName splits a tool name such as "myapp_sub_command" into its
// underscore-delimited segments: ["myapp", "sub", "command"].
// It is the inverse of the encoding performed by toolName in selector.go.
func splitToolName(name string) []string {
	return strings.Split(name, "_")
}

// buildCommandArgs constructs CLI arguments from the MCP request.
// It decodes the tool name back into a command path and appends flags and
// positional arguments so the resulting slice can be passed directly to
// exec.Command when re-invoking the binary as a subprocess.
//
// Positional arguments are taken from input.Args (map[string]any), which is
// populated when the command schema uses named argument tokens. The map is
// flattened into an ordered []string by resolvePositionalArgs.
func buildCommandArgs(name string, input ToolInput) []string {
	// Decode "root_sub_command" -> ["sub", "command"] by dropping the root prefix.
	args := splitToolName(name)[1:]

	// Add flags
	flagArgs := buildFlagArgs(input.Flags)
	args = append(args, flagArgs...)

	// Add positional arguments.
	positional := resolvePositionalArgs(input)
	return append(args, positional...)
}

// resolvePositionalArgs extracts an ordered positional-argument list from a
// ToolInput by flattening the Args map into a string slice.
//
// Note: map iteration order in Go is non-deterministic; for the subprocess
// model the spec order is not available at this layer, so values are collected
// in sorted-key order as a best-effort fallback. Callers that need a
// guaranteed order should use the in-process model where buildInProcessArgs
// has access to the ArgSpec slice.
func resolvePositionalArgs(input ToolInput) []string {
	if len(input.Args) > 0 {
		return flattenPositionalArgsMap(input.Args)
	}
	return nil
}

// flattenPositionalArgsMap converts a positional-args map into an ordered
// string slice. Values that are themselves slices (variadic args) are expanded
// inline. The iteration is sorted by key name to produce a deterministic result
// in the subprocess model; the in-process model uses collectPositionalArgs
// instead, which respects ArgSpec ordering.
func flattenPositionalArgsMap(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}

	// Collect keys in sorted order for deterministic output.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Use a simple insertion-sort since the number of args is tiny.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	var out []string
	for _, k := range keys {
		val := m[k]
		if val == nil {
			continue
		}
		switch v := val.(type) {
		case []any:
			for _, item := range v {
				out = append(out, fmt.Sprintf("%v", item))
			}
		case []string:
			out = append(out, v...)
		default:
			out = append(out, fmt.Sprintf("%v", val))
		}
	}
	return out
}

// buildFlagArgs converts the flags map from a [ToolInput] into a slice of CLI
// flag arguments understood by cobra/pflag.
//
// Conversion rules:
//   - bool true  → "--flag"       (false is omitted)
//   - scalar     → "--flag value"
//   - []any      → "--flag a" "--flag b" (repeated, one per element)
//   - map[string]any → "--flag key=value" (stringToString style)
func buildFlagArgs(flagMap map[string]any) []string {
	var args []string

	for name, value := range flagMap {
		if name == "" || value == nil {
			continue
		}

		// Slice: emit one --flag per element (pflag slice / repeated flags).
		if items, ok := value.([]any); ok {
			for _, item := range items {
				args = append(args, parseFlagArgValue(name, item)...)
			}

			continue
		}

		// Map: emit --flag key=value entries (pflag stringToString).
		if mapVal, ok := value.(map[string]any); ok {
			for k, v := range mapVal {
				args = append(args, fmt.Sprintf("--%s", name), fmt.Sprintf("%s=%v", k, v))
			}

			continue
		}

		args = append(args, parseFlagArgValue(name, value)...)
	}

	return args
}

// parseFlagArgValue converts a single flag value to CLI argument tokens.
// A bool true yields ["--name"]; any other type yields ["--name", "value"].
// A bool false or nil value yields an empty slice (flag is omitted).
func parseFlagArgValue(name string, value any) (retVal []string) {
	if value != nil {
		switch v := value.(type) {
		case bool:
			if v {
				retVal = append(retVal, fmt.Sprintf("--%s", name))
			}
		default:
			retVal = append(retVal, fmt.Sprintf("--%s", name), fmt.Sprintf("%v", value))
		}
	}

	return retVal
}
