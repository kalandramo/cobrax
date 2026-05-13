package main

// This test file verifies that each command's positional arguments are correctly
// reflected in the generated MCP Tool JSON Schema.
//
// For each pattern we check:
//   - The "args" object property is present when the command has positional args.
//   - Each argument token maps to a correctly-typed property.
//   - Required tokens appear in the "required" list; optional ones do not.
//   - Variadic tokens map to an array-of-string property.
//   - Annotation-supplied descriptions are preserved verbatim.

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/onexstack/cobrax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 1 – oncall <module> [title]
// ─────────────────────────────────────────────────────────────────────────────

func TestOncallSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	schema := findToolInputSchema(t, srv, "oncall")

	// "args" object must be present; "positional_args" must not.
	require.Contains(t, schema.Properties, "args",
		"oncall: expected args object in schema")
	assert.NotContains(t, schema.Properties, "positional_args",
		"oncall: positional_args key must not exist")

	pos := schema.Properties["args"]
	require.Equal(t, "object", pos.Type)

	// <module> — required, string
	require.Contains(t, pos.Properties, "module")
	assert.Equal(t, "string", pos.Properties["module"].Type)
	assert.Contains(t, pos.Required, "module",
		"<module> is required")

	// [title] — optional, string
	require.Contains(t, pos.Properties, "title")
	assert.Equal(t, "string", pos.Properties["title"].Type)
	assert.NotContains(t, pos.Required, "title",
		"[title] is optional")

	// Annotation descriptions must be preserved.
	assert.Contains(t, pos.Properties["module"].Description, "bke",
		"module description should mention example values from annotation")
	assert.Contains(t, pos.Properties["title"].Description, "可选",
		"title description should mention it is optional")
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 2 – cp <src> <dst>
// ─────────────────────────────────────────────────────────────────────────────

func TestCpSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	schema := findToolInputSchema(t, srv, "cp")

	require.Contains(t, schema.Properties, "args")
	pos := schema.Properties["args"]

	// Both args must be required strings.
	for _, name := range []string{"src", "dst"} {
		require.Contains(t, pos.Properties, name, "cp: property %q missing", name)
		assert.Equal(t, "string", pos.Properties[name].Type)
		assert.Contains(t, pos.Required, name, "cp: %q must be required", name)
	}

	// Annotation descriptions.
	assert.Contains(t, pos.Properties["src"].Description, "源文件")
	assert.Contains(t, pos.Properties["dst"].Description, "目标")
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 3 – make [targets...]
// ─────────────────────────────────────────────────────────────────────────────

func TestMakeSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	schema := findToolInputSchema(t, srv, "make")

	// Variadic arg → args object with an array property.
	require.Contains(t, schema.Properties, "args")
	pos := schema.Properties["args"]

	require.Contains(t, pos.Properties, "targets")
	targetsSchema := pos.Properties["targets"]
	assert.Equal(t, "array", targetsSchema.Type,
		"make: [targets...] should map to array type")
	require.NotNil(t, targetsSchema.Items)
	assert.Equal(t, "string", targetsSchema.Items.Type)

	// [targets...] is optional → not in required.
	assert.NotContains(t, pos.Required, "targets",
		"make: [targets...] is optional, must not be required")

	// Annotation description should be present.
	assert.Contains(t, targetsSchema.Description, "Makefile")
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 4 – kubectl sub-command tree
// ─────────────────────────────────────────────────────────────────────────────

func TestKubectlGetSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	// Tool name for "kubectl get" with empty prefix → "kubectl_get"
	schema := findToolInputSchema(t, srv, "kubectl_get")

	require.Contains(t, schema.Properties, "args")
	pos := schema.Properties["args"]

	// <resource> required, [name] optional
	require.Contains(t, pos.Properties, "resource")
	assert.Equal(t, "string", pos.Properties["resource"].Type)
	assert.Contains(t, pos.Required, "resource")

	require.Contains(t, pos.Properties, "name")
	assert.Equal(t, "string", pos.Properties["name"].Type)
	assert.NotContains(t, pos.Required, "name")

	// Flags should still be present alongside args.
	require.Contains(t, schema.Properties, "flags")
	flagsSchema := schema.Properties["flags"]
	assert.Contains(t, flagsSchema.Properties, "namespace")
	assert.Contains(t, flagsSchema.Properties, "output")
}

func TestKubectlLogsSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	// "logs <pod> [flags]" — [flags] is ignored by cobrax.
	schema := findToolInputSchema(t, srv, "kubectl_logs")
	require.Contains(t, schema.Properties, "args")
	pos := schema.Properties["args"]

	require.Contains(t, pos.Properties, "pod")
	assert.Equal(t, "string", pos.Properties["pod"].Type)
	assert.Contains(t, pos.Required, "pod")

	// [flags] must NOT appear as a property.
	assert.NotContains(t, pos.Properties, "flags",
		"[flags] sentinel must be ignored by cobrax")
}

func TestKubectlExecSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	// "exec <pod> <cmd...>" — pod required string, cmd required string array
	schema := findToolInputSchema(t, srv, "kubectl_exec")
	require.Contains(t, schema.Properties, "args")
	pos := schema.Properties["args"]

	require.Contains(t, pos.Properties, "pod")
	assert.Equal(t, "string", pos.Properties["pod"].Type)
	assert.Contains(t, pos.Required, "pod")

	require.Contains(t, pos.Properties, "cmd")
	cmdSchema := pos.Properties["cmd"]
	assert.Equal(t, "array", cmdSchema.Type,
		"exec: <cmd...> should be a string array")
	require.NotNil(t, cmdSchema.Items)
	assert.Equal(t, "string", cmdSchema.Items.Type)
	assert.Contains(t, pos.Required, "cmd",
		"exec: <cmd...> is required")
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 5 – chat (no positional args)
// ─────────────────────────────────────────────────────────────────────────────

func TestChatSchema(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	schema := findToolInputSchema(t, srv, "chat")

	// chat has no positional arg tokens → "args" must be absent.
	assert.NotContains(t, schema.Properties, "args",
		"chat: args must be absent when the command has no positional arg tokens")
	assert.NotContains(t, schema.Properties, "positional_args",
		"chat: positional_args must never exist")

	// flags must still be present (--test flag).
	require.Contains(t, schema.Properties, "flags")
	assert.Contains(t, schema.Properties["flags"].Properties, "test")
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool inventory
// ─────────────────────────────────────────────────────────────────────────────

func TestToolInventory(t *testing.T) {
	root := buildRootCmd()
	root.AddCommand(cobrax.Command(nil))

	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{Enabled: false, Name: "myops"}, root)
	require.NoError(t, err)

	tools := srv.Tools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	expected := []string{
		"oncall",
		"cp",
		"make",
		"kubectl_get",
		"kubectl_logs",
		"kubectl_exec",
	}
	for _, e := range expected {
		assert.Contains(t, names, e, "expected tool %q to be registered", e)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helper: find a tool's input schema by its name suffix
// ─────────────────────────────────────────────────────────────────────────────

func findToolInputSchema(t *testing.T, srv *cobrax.MCPServer, toolName string) *jsonschema.Schema {
	t.Helper()
	for _, tool := range srv.Tools() {
		if tool.Name == toolName {
			require.NotEmpty(t, tool.RawInputSchema, "tool %q: RawInputSchema must not be empty", toolName)
			s := &jsonschema.Schema{}
			require.NoError(t, json.Unmarshal(tool.RawInputSchema, s))
			return s
		}
	}
	names := make([]string, 0)
	for _, tool := range srv.Tools() {
		names = append(names, tool.Name)
	}
	t.Fatalf("tool %q not found; registered tools: %v", toolName, names)
	return nil
}
