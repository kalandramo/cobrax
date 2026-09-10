# Schema Generation

cobrax automatically generates JSON schemas for MCP tools from Cobra commands.
Flags and positional arguments are flattened into **top-level properties** —
there is no nested `flags` / `args` object.

## Tool Properties

- **Name**: `CommandPath()` with the root name replaced by `ToolNamePrefix`
  (shortened to satisfy Claude's 64-char limit), spaces → underscores
  (e.g. `myapp sre open` → `myapp_sre_open`). The in-process model uses an empty
  prefix, so tool names never include the (possibly illegal) root command name.
- **Description**: `Long` > `Short` > fallback `"Execute the X command"`,
  with `Example` appended. Long/Example are the primary channel for teaching
  the LLM how to call the tool.
- **Annotations**: `title`, `readOnlyHint`, `destructiveHint`, `idempotentHint`,
  `openWorldHint` are read from `cmd.Annotations` (booleans parsed with
  `strconv.ParseBool`).

## Input Schema (flat)

```json
{
  "type": "object",
  "properties": {
    "namespace": { "type": "string", "description": "Kubernetes namespace", "default": "default" },
    "resource":  { "type": "string", "description": "Resource type (required)…" },
    "name":      { "type": "string", "description": "Resource name (optional)…" }
  },
  "required": ["resource"],
  "additionalProperties": { "not": {} }
}
```

`additionalProperties: not{}` forbids undeclared fields. The registration-time
metadata (`toolMeta`: flag property names + ordered arg specs) is generated
together with the schema — the two must stay in sync so the flat map can be
split back into CLI arguments at execution time.

### Flag type mapping

| pflag type | JSON Schema |
|------------|-------------|
| `bool` | `boolean` |
| `int*` / `uint*` / `count` | `integer` |
| `float32`/`float64` | `number` |
| `string` | `string`; if the flag has a `jsonschema` annotation, replaced wholesale by that custom schema |
| slices (`stringSlice`, `intSlice`, …) | `array` |
| `stringToString` | `object` (additionalProperties: string) |
| `stringToInt(64)` | `object` (additionalProperties: integer) |
| `duration` | `string` + pattern `^-?([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$` |
| `ip` / `ipNet` / `bytesHex` / `bytesBase64` | `string` + pattern |
| unknown | `string` + `(type: xxx)` in description |

- **required**: read from cobra's `MarkFlagRequired` annotation.
- **defaults**: parsed from `flag.DefValue` (pflag array defaults like `"[a,b]"`
  are split and typed; `"[]"` means empty).
- **Custom schema escape hatch**: a string flag may carry a `jsonschema`
  annotation holding a full JSON Schema (e.g. built with
  `jsonschema.For[SomeStruct](nil)`), letting the LLM pass a whole JSON object:

```go
schema, _ := jsonschema.For[SomeJsonObject](nil)
bytes, _ := schema.MarshalJSON()
f := cmd.Flags().Lookup("a_json_obj")
f.Annotations = map[string][]string{"jsonschema": {string(bytes)}}
```

### Positional arguments

Parsed from `cmd.Use` token syntax:

| cmd.Use | Meaning | Schema |
|---------|---------|--------|
| `<module>` | required single | `string`, in `required` |
| `[title]` | optional single | `string` |
| `[targets...]` / `<files...>` | variadic | `array` of string |
| `[flags]` | cobra sentinel | silently skipped |

Argument descriptions (critical for LLM fill quality) come from annotations —
index-based, priority over the synthesized fallback:

```go
cmd.Annotations = map[string]string{
    cobrax.AnnotationArgPrefix + "0": "The on-call module (bke, kafka, redis)…",
    cobrax.AnnotationArgPrefix + "1": "Brief incident title…",
}
```

Cobra `Args` validators (`RangeArgs`, …) do not participate in schema generation;
the underlying command validates at execution time.

## Output Schema

```json
{
  "type": "object",
  "properties": {
    "stdout": { "type": "string" },
    "stderr": { "type": "string" },
    "exitCode": { "type": "integer" }
  }
}
```

## Export Schemas

```bash
./my-cli mcp tools  # Creates mcp-tools.json
```
