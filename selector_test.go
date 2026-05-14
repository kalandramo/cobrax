package cobrax

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hasSchemaType checks if a schema has the specified type.
// In jsonschema-go v0.4.2+, nullable types use Types []string instead of Type string.
// For example, []string with omitempty becomes Types: ["null", "array"] instead of Type: "array".
func hasSchemaType(schema *jsonschema.Schema, expectedType string) bool {
	if schema.Type == expectedType {
		return true
	}
	return slices.Contains(schema.Types, expectedType)
}

// buildCommandTree creates a command tree from a list of command names.
// The first command becomes the root, and subsequent commands are nested.
func buildCommandTree(names ...string) *cobra.Command {
	if len(names) == 0 {
		return nil
	}

	root := &cobra.Command{Use: names[0]}
	parent := root

	for _, name := range names[1:] {
		child := &cobra.Command{
			Use: name,
			Run: func(_ *cobra.Command, _ []string) {},
		}

		parent.AddCommand(child)
		parent = child
	}

	return parent
}

type SomeJSONObject struct {
	Foo    string
	Bar    int
	FooBar struct {
		Baz string
	}
}

type SomeJSONArray []SomeJSONObject

// parseRawInputSchema deserializes the tool's RawInputSchema into a jsonschema.Schema.
func parseRawInputSchema(t *testing.T, rawSchema json.RawMessage) *jsonschema.Schema {
	t.Helper()
	require.NotNil(t, rawSchema, "RawInputSchema must not be nil")
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(rawSchema, &schema))
	return &schema
}

func TestCreateToolFromCmd(t *testing.T) {
	// Create a simple test command
	cmd := &cobra.Command{
		Use:     "test [file]",
		Short:   "Test command",
		Long:    "This is a test command for testing the cobrax package",
		Example: "test file.txt --output result.txt",
	}

	// Add some flags
	cmd.Flags().String("output", "", "Output file")
	cmd.Flags().Bool("verbose", false, "Verbose output")
	cmd.Flags().IntSlice("include", []int{}, "Include patterns")
	cmd.Flags().StringSlice("greeting", []string{"hello", "world"}, "Include patterns")
	cmd.Flags().Int("count", 10, "Number of items")
	cmd.Flags().StringToString("labels", map[string]string{"hello": "world", "go": "lang"}, "Key-value labels")
	cmd.Flags().StringToInt("ports", map[string]int{"life": 42, "power": 9001}, "Port mappings")

	// generate schema for a test object
	aJSONObjSchema, err := jsonschema.For[SomeJSONObject](nil)
	require.NoError(t, err)
	bytes, err := aJSONObjSchema.MarshalJSON()
	require.NoError(t, err)

	// now create flag that has a json schema that represents a json object
	cmd.Flags().String("a_json_obj", "", "Some JSON Object")
	jsonobj := cmd.Flags().Lookup("a_json_obj")
	jsonobj.Annotations = make(map[string][]string)
	jsonobj.Annotations["jsonschema"] = []string{string(bytes)}

	// generate schema for a test array
	aJSONArraySchema, err := jsonschema.For[SomeJSONArray](nil)
	require.NoError(t, err)
	bytes, err = aJSONArraySchema.MarshalJSON()
	require.NoError(t, err)

	// now create flag that has a json schema that represents a json array
	// note that we can supply a default for the flag here but it's not mapped to the schema default
	cmd.Flags().String("a_json_array", "[]", "Some JSON Array")
	jsonarray := cmd.Flags().Lookup("a_json_array")
	jsonarray.Annotations = make(map[string][]string)
	jsonarray.Annotations["jsonschema"] = []string{string(bytes)}

	// Add a hidden flag
	cmd.Flags().String("hidden", "secret", "Hidden flag")
	err = cmd.Flags().MarkHidden("hidden")
	require.NoError(t, err)

	// Add a deprecated flag
	cmd.Flags().String("old", "", "Old flag")
	err = cmd.Flags().MarkDeprecated("old", "Use --new instead")
	require.NoError(t, err)

	// Mark one flag as required
	err = cmd.MarkFlagRequired("count")
	require.NoError(t, err)

	parent := &cobra.Command{
		Use:   "parent",
		Short: "Parent command",
	}

	// add persistent flag to parent
	parent.PersistentFlags().String("config", "", "Config file")
	parent.AddCommand(cmd)

	t.Run("Default Selector", func(t *testing.T) {
		// Create tool from command with a selector that accepts all flags.
		tool, meta := Selector{}.createToolFromCmd(cmd, "parent")

		// Verify tool properties
		assert.Equal(t, "parent_test", tool.Name)
		assert.Contains(t, tool.Description, "This is a test command")
		assert.Contains(t, tool.Description, "test file.txt --output result.txt")
		assert.NotNil(t, tool.RawInputSchema)

		// Parse the raw input schema for inspection.
		inputSchema := parseRawInputSchema(t, tool.RawInputSchema)
		assert.Equal(t, "object", inputSchema.Type)
		require.NotNil(t, inputSchema.Properties)

		// In the flat schema format there are no nested "flags" or "args" wrappers;
		// all parameters are direct top-level properties.
		assert.NotContains(t, inputSchema.Properties, "flags",
			"Flat schema must not have a nested 'flags' property")
		assert.NotContains(t, inputSchema.Properties, "args",
			"Flat schema must not have a nested 'args' property")

		// All flag properties should be at the top level.
		assert.Contains(t, inputSchema.Properties, "output")
		assert.Contains(t, inputSchema.Properties, "verbose")
		assert.Contains(t, inputSchema.Properties, "include")
		assert.Contains(t, inputSchema.Properties, "count")
		assert.Contains(t, inputSchema.Properties, "greeting")
		assert.Contains(t, inputSchema.Properties, "labels")
		assert.Contains(t, inputSchema.Properties, "ports")
		assert.Contains(t, inputSchema.Properties, "a_json_obj")
		assert.Contains(t, inputSchema.Properties, "a_json_array")

		// Verify excluded flags
		assert.NotContains(t, inputSchema.Properties, "hidden", "Should not include hidden flag")
		assert.NotContains(t, inputSchema.Properties, "old", "Should not include deprecated flag")

		// Verify flag types at the top level.
		assert.Equal(t, "string", inputSchema.Properties["output"].Type)
		assert.Equal(t, "boolean", inputSchema.Properties["verbose"].Type)
		assert.Equal(t, "array", inputSchema.Properties["include"].Type)
		assert.Equal(t, "integer", inputSchema.Properties["count"].Type)
		assert.Equal(t, "array", inputSchema.Properties["greeting"].Type)
		assert.Equal(t, "object", inputSchema.Properties["labels"].Type)
		assert.Equal(t, "object", inputSchema.Properties["ports"].Type)
		assert.Equal(t, "object", inputSchema.Properties["a_json_obj"].Type)
		assert.True(t, hasSchemaType(inputSchema.Properties["a_json_array"], "array"), "a_json_array should be an array type")

		// Verify required flags appear at the top-level required list.
		assert.Contains(t, inputSchema.Required, "count", "count flag should be marked as required")

		// Verify default values
		assert.NotNil(t, inputSchema.Properties["verbose"].Default)
		assert.JSONEq(t, "false", string(inputSchema.Properties["verbose"].Default))
		assert.NotNil(t, inputSchema.Properties["count"].Default)
		assert.JSONEq(t, "10", string(inputSchema.Properties["count"].Default))
		assert.NotNil(t, inputSchema.Properties["greeting"].Default)
		assert.JSONEq(t, `["hello","world"]`, string(inputSchema.Properties["greeting"].Default))
		assert.JSONEq(t, `{"life":42, "power":9001}`, string(inputSchema.Properties["ports"].Default))
		assert.JSONEq(t, `{"hello":"world", "go":"lang"}`, string(inputSchema.Properties["labels"].Default))
		// Empty string and empty array should not have defaults set
		assert.Nil(t, inputSchema.Properties["output"].Default)
		assert.Nil(t, inputSchema.Properties["include"].Default)

		// json schema defaults are not populated
		assert.Nil(t, inputSchema.Properties["a_json_obj"].Default)
		assert.Nil(t, inputSchema.Properties["a_json_array"].Default)

		// verify json obj schemas - compare key fields rather than full schema
		// because PropertyOrder handling changed between jsonschema-go versions
		parsedJSONObjSchema := inputSchema.Properties["a_json_obj"]
		assert.Equal(t, aJSONObjSchema.Type, parsedJSONObjSchema.Type)
		assert.Equal(t, aJSONObjSchema.Required, parsedJSONObjSchema.Required)
		assert.Equal(t, len(aJSONObjSchema.Properties), len(parsedJSONObjSchema.Properties))

		// Verify array items schema
		includeSchema := inputSchema.Properties["include"]
		assert.NotNil(t, includeSchema.Items)
		assert.Equal(t, "integer", includeSchema.Items.Type)
		greetingSchema := inputSchema.Properties["greeting"]
		assert.NotNil(t, greetingSchema.Items)
		assert.Equal(t, "string", greetingSchema.Items.Type)

		// Verify stringToString object schema
		labelsSchema := inputSchema.Properties["labels"]
		assert.NotNil(t, labelsSchema.AdditionalProperties)
		assert.Equal(t, "string", labelsSchema.AdditionalProperties.Type)

		// Verify stringToInt object schema
		portsSchema := inputSchema.Properties["ports"]
		assert.NotNil(t, portsSchema.AdditionalProperties)
		assert.Equal(t, "integer", portsSchema.AdditionalProperties.Type)

		// Verify persistent flag from parent command appears at the top level.
		assert.Contains(t, inputSchema.Properties, "config",
			"Should include persistent flag from parent command")

		// cmd.Use = "test [file]" → parsed as optional named arg "file"
		// In the flat schema this appears as a direct top-level property.
		assert.Contains(t, inputSchema.Properties, "file",
			"Should have 'file' property at top level when cmd.Use has named arg tokens")
		assert.NotContains(t, inputSchema.Properties, "positional_args",
			"positional_args key should not exist")

		fileArgSchema := inputSchema.Properties["file"]
		assert.Equal(t, "string", fileArgSchema.Type)
		// [file] is optional → not in Required list
		assert.NotContains(t, inputSchema.Required, "file",
			"Optional arg [file] should not be required")

		// Verify meta contains the expected flag names and arg specs.
		assert.Contains(t, meta.flagNames, "output")
		assert.Contains(t, meta.flagNames, "verbose")
		assert.Contains(t, meta.flagNames, "count")
		assert.Contains(t, meta.flagNames, "config")
		assert.NotContains(t, meta.flagNames, "file", "positional arg should not be in flagNames")
		require.Len(t, meta.argSpecs, 1)
		assert.Equal(t, "file", meta.argSpecs[0].Name)
	})

	t.Run("Restricted Selector", func(t *testing.T) {
		// Create a selector that only allows specific flags
		selector := Selector{
			LocalFlagSelector: func(flag *pflag.Flag) bool {
				names := []string{"output", "verbose", "hidden", "old"}
				return slices.Contains(names, flag.Name)
			},
			InheritedFlagSelector: func(_ *pflag.Flag) bool { return false },
		}

		// Create tool from command with the restricted selector
		tool, _ := selector.createToolFromCmd(cmd, "parent")

		// Verify tool properties
		assert.Equal(t, "parent_test", tool.Name)
		assert.Contains(t, tool.Description, "This is a test command")
		assert.Contains(t, tool.Description, "test file.txt --output result.txt")
		assert.NotNil(t, tool.RawInputSchema)

		// Parse the raw input schema for inspection.
		inputSchema := parseRawInputSchema(t, tool.RawInputSchema)
		assert.Equal(t, "object", inputSchema.Type)
		require.NotNil(t, inputSchema.Properties)

		// Flat schema — no nested wrappers.
		assert.NotContains(t, inputSchema.Properties, "flags")
		assert.NotContains(t, inputSchema.Properties, "args")

		assert.Contains(t, inputSchema.Properties, "output")
		assert.Contains(t, inputSchema.Properties, "verbose")

		// Verify excluded flags
		assert.NotContains(t, inputSchema.Properties, "hidden", "Should not include hidden flag")
		assert.NotContains(t, inputSchema.Properties, "old", "Should not include deprecated flag")
		assert.NotContains(t, inputSchema.Properties, "include", "Should not include excluded flag")
		assert.NotContains(t, inputSchema.Properties, "count", "Should not include excluded flag")
		assert.NotContains(t, inputSchema.Properties, "config", "Should not include excluded persistent flag")
		assert.NotContains(t, inputSchema.Properties, "greeting", "Should not include excluded flag")
		assert.NotContains(t, inputSchema.Properties, "labels", "Should not include excluded flag")
		assert.NotContains(t, inputSchema.Properties, "ports", "Should not include excluded flag")

		// Verify required flags - none should be required since 'count' was excluded
		assert.NotContains(t, inputSchema.Required, "count", "Excluded count flag should not be required")

		// Positional arg from cmd.Use = "test [file]" should still appear.
		assert.Contains(t, inputSchema.Properties, "file")
		assert.NotContains(t, inputSchema.Properties, "positional_args")
	})

	t.Run("No positional args - no extra property", func(t *testing.T) {
		// A command with no positional argument tokens in cmd.Use should have
		// neither an "args" nor a "positional_args" property in its schema.
		noArgCmd := &cobra.Command{
			Use:   "noarg",
			Short: "No positional args",
			Run:   func(_ *cobra.Command, _ []string) {},
		}
		tool, _ := Selector{}.createToolFromCmd(noArgCmd, "root")
		schema := parseRawInputSchema(t, tool.RawInputSchema)
		assert.NotContains(t, schema.Properties, "args",
			"Should not have args property when cmd.Use has no named arg tokens")
		assert.NotContains(t, schema.Properties, "positional_args",
			"Should not have positional_args property")
	})

	t.Run("Required positional arg", func(t *testing.T) {
		reqArgCmd := &cobra.Command{
			Use:   "create <name>",
			Short: "Create something",
			Run:   func(_ *cobra.Command, _ []string) {},
		}
		tool, _ := Selector{}.createToolFromCmd(reqArgCmd, "root")
		schema := parseRawInputSchema(t, tool.RawInputSchema)
		// In flat schema the positional arg is a direct top-level property.
		require.Contains(t, schema.Properties, "name")
		assert.Contains(t, schema.Required, "name",
			"Required arg <name> should be in top-level Required list")
	})

	t.Run("Variadic positional arg", func(t *testing.T) {
		varArgCmd := &cobra.Command{
			Use:   "run <targets...>",
			Short: "Run targets",
			Run:   func(_ *cobra.Command, _ []string) {},
		}
		tool, _ := Selector{}.createToolFromCmd(varArgCmd, "root")
		schema := parseRawInputSchema(t, tool.RawInputSchema)
		// In flat schema the positional arg is a direct top-level property.
		require.Contains(t, schema.Properties, "targets")
		assert.Equal(t, "array", schema.Properties["targets"].Type,
			"Variadic arg should be an array type")
		assert.Contains(t, schema.Required, "targets")
	})

	t.Run("Positional arg with annotation description", func(t *testing.T) {
		annotCmd := &cobra.Command{
			Use:   "oncall <module> [title]",
			Short: "Create on-call ticket",
			Run:   func(_ *cobra.Command, _ []string) {},
			Annotations: map[string]string{
				AnnotationArgPrefix + "0": "The on-call module name (e.g. 'bke', 'kafka')",
				AnnotationArgPrefix + "1": "Brief title describing the incident",
			},
		}
		tool, _ := Selector{}.createToolFromCmd(annotCmd, "root")
		schema := parseRawInputSchema(t, tool.RawInputSchema)
		// Both positional args appear as flat top-level properties.
		require.Contains(t, schema.Properties, "module")
		require.Contains(t, schema.Properties, "title")
		assert.Equal(t, "The on-call module name (e.g. 'bke', 'kafka')",
			schema.Properties["module"].Description)
		assert.Equal(t, "Brief title describing the incident",
			schema.Properties["title"].Description)
		assert.Contains(t, schema.Required, "module")
		assert.NotContains(t, schema.Required, "title")
	})
}

func TestGenerateToolName(t *testing.T) {
	root := &cobra.Command{
		Use: "root",
	}
	child := &cobra.Command{
		Use: "child",
	}
	grandchild := &cobra.Command{
		Use: "grandchild",
	}

	root.AddCommand(child)
	child.AddCommand(grandchild)

	t.Run("Default prefix (uses root name)", func(t *testing.T) {
		name := toolName(grandchild, "root")
		assert.Equal(t, "root_child_grandchild", name)
	})

	t.Run("Custom short prefix", func(t *testing.T) {
		name := toolName(grandchild, "r")
		assert.Equal(t, "r_child_grandchild", name)
	})

	t.Run("Root command only", func(t *testing.T) {
		name := toolName(root, "root")
		assert.Equal(t, "root", name)
	})

	t.Run("Root command with custom prefix", func(t *testing.T) {
		name := toolName(root, "myprefix")
		assert.Equal(t, "myprefix", name)
	})

	t.Run("Omnistrate use case - shortening long tool names", func(t *testing.T) {
		// Simulates: omnistrate-ctl cost by-instance-type in-provider
		omctl := &cobra.Command{Use: "omnistrate-ctl"}
		cost := &cobra.Command{Use: "cost"}
		byInstanceType := &cobra.Command{Use: "by-instance-type"}
		inProvider := &cobra.Command{Use: "in-provider", Run: func(_ *cobra.Command, _ []string) {}}

		omctl.AddCommand(cost)
		cost.AddCommand(byInstanceType)
		byInstanceType.AddCommand(inProvider)

		// Using full root name (original behavior)
		fullName := toolName(inProvider, "omnistrate-ctl")
		assert.Equal(t, "omnistrate-ctl_cost_by-instance-type_in-provider", fullName)

		// Using shortened prefix - saves 9 characters (len("omnistrate-ctl") - len("omctl") = 14 - 5 = 9)
		shortName := toolName(inProvider, "omctl")
		assert.Equal(t, "omctl_cost_by-instance-type_in-provider", shortName)
		assert.Less(t, len(shortName), len(fullName), "Short name should be shorter than full name")
		assert.Less(t, len(shortName), 64, "Short name should be under Claude's 64-char limit")
	})
}

func TestGenerateToolDescription(t *testing.T) {
	t.Run("Long and Example", func(t *testing.T) {
		cmd1 := &cobra.Command{
			Use:     "cmd1",
			Short:   "Short description",
			Long:    "Long description of cmd1",
			Example: "cmd1 --help",
		}
		desc1 := toolDescription(cmd1)
		assert.Contains(t, desc1, "Long description of cmd1")
		assert.Contains(t, desc1, "Examples:\ncmd1 --help")
	})

	t.Run("Short only", func(t *testing.T) {
		cmd2 := &cobra.Command{
			Use:   "cmd2",
			Short: "Short description of cmd2",
		}
		desc2 := toolDescription(cmd2)
		assert.Equal(t, "Short description of cmd2", desc2)
	})
}
