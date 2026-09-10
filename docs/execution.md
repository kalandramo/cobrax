# Tool Execution

When an AI assistant calls an MCP tool, cobrax turns the flat JSON arguments back
into CLI arguments and runs the command. Two execution models are supported.

## Flat Input

MCP clients send **all parameters (flags and positional arguments) as flat
top-level properties** — there is no nested `flags` / `args` object:

```json
{
  "name": "myapp_get_pods",
  "arguments": {
    "namespace": "production",
    "output": "json",
    "resource": "pods",
    "name": "web-server"
  }
}
```

At registration time cobrax records a `toolMeta` (the flag property names and the
ordered positional-arg specs) alongside the generated schema. At execution time
`splitFlatInput` uses that metadata to split the flat map back into flags and
positional arguments:

```
FlatInput (flat map)
    ├── keys hit by FlagNames → flagMap
    └── keys in ArgNames order → posArgs (variadic arrays expanded)
    ↓
buildFlagArgs → CLI flag token sequence
```

## Execution Flow

1. **Middleware** (optional, per-selector) — wraps execution, always guarded by `recover`
2. **Execution** — one of:

| Model | How it runs | Best for |
|-------|-------------|----------|
| Subprocess (`cobrax.Command`) | Re-invokes `os.Executable()` as a child process per call | CLIs whose state is fully rebuildable from flags (`make`, `kubectl` wrappers) |
| In-process (`cobrax.NewMCPServer`) | Calls a command-tree factory, builds a fresh tree, then `ExecuteContext` | Commands whose closures hold non-serializable deps (DB handles, API clients) |

The in-process model runs `root.ExecuteContext(context.WithoutCancel(ctx))`:
when the AI runner cancels the context right after receiving the first result,
in-flight I/O (e.g. an external API call) is not killed mid-side-effect.

## Command Construction

**Subprocess model:** the tool name is split on underscores, the first segment
(root command name) is dropped, and the remaining segments form the command path:

```bash
/path/to/myapp get pods --namespace production --output json web-server
```

**In-process model:** the command path is pre-recorded at registration time
(`toolPaths`), because tool names may be rewritten via `ToolNamePrefix` or contain
characters that make name-splitting lossy.

**Flag value conversion:**

| Input value | CLI arguments |
|-------------|---------------|
| `true` | `--flag` |
| `false` / `null` | omitted |
| scalar | `--flag value` |
| `["a", "b"]` | `--flag a --flag b` (repeated flag, pflag slice semantics) |
| `{"k": "v"}` | `--flag k=v` (stringToString semantics) |

## Output

Executions return stdout, stderr and the exit code. Non-zero exit codes are
normalized into an MCP tool error (with the full output attached) — command
business failures and execution failures are indistinguishable to the LLM, which
can always read the complete output.

## Cancellation

- Subprocess model: MCP client cancellation cancels the context, which kills the
  child process and returns an error.
- Middleware can short-circuit by returning early without calling `next`.
