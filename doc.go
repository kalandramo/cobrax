// Package cobrax transforms Cobra CLI applications into MCP (Model Context Protocol) servers,
// enabling AI assistants to interact with command-line tools.
//
// Ophis automatically converts existing Cobra commands into MCP tools, handling
// protocol complexity, command execution, and tool registration.
//
// # Two Execution Models
//
// Ophis supports two execution models:
//
//  1. Subprocess model ([Command] / [Config]): The MCP server re-invokes the
//     same binary as a subprocess for each tool call. This works well when all
//     necessary state can be reconstructed from CLI flags alone.
//
//  2. In-process model ([NewMCPServer] / [MCPServer]): The MCP server calls the
//     cobra.Command's Run/RunE function directly in-process. Because the command
//     closures already hold references to runtime dependencies (API clients,
//     database handles, business-logic objects, etc.), those objects are
//     available without any additional wiring. This is the preferred model when
//     commands depend on non-serialisable state.
//
// # In-Process Usage (NewMCPServer)
//
// Construct the cobra command tree with its dependencies already injected, then
// pass the root command to NewMCPServer:
//
//	package main
//
//	import (
//	    "context"
//	    "log"
//	    "github.com/onexstack/cobrax"
//	)
//
//	func main() {
//	    // Dependencies are injected at construction time.
//	    biz := newBotSreBiz(apiClient, dbClient)
//
//	    root := buildRootCommand(biz) // adds all subcommands with biz in closures
//
//	    srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{
//	        Enabled: true,
//	        Addr:    ":8090",
//	        Name:    "myapp",
//	        Version: "1.0.0",
//	    }, root)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//
//	    if err := srv.Start(context.Background()); err != nil {
//	        log.Fatal(err)
//	    }
//	}
//
// # Subprocess Usage (Command)
//
// Add MCP server management subcommands to an existing Cobra application:
//
//	package main
//
//	import (
//	    "os"
//	    "github.com/onexstack/cobrax"
//	)
//
//	func main() {
//	    rootCmd := createMyRootCommand()
//
//	    // Adds: mcp start, mcp tools, mcp claude enable/disable/list, etc.
//	    rootCmd.AddCommand(cobrax.Command(nil))
//
//	    if err := rootCmd.Execute(); err != nil {
//	        os.Exit(1)
//	    }
//	}
//
// # Configuration (Subprocess model)
//
// The [Config] struct provides fine-grained control over which commands and
// flags are exposed as MCP tools through a selector system.
//
// Basic filters are always applied automatically:
//   - Hidden and deprecated commands/flags are excluded
//   - Commands without executable functions are excluded
//   - Built-in commands (mcp, help, completion) are excluded
//
// Example with selectors:
//
//	config := &cobrax.Config{
//	    Selectors: []cobrax.Selector{
//	        {
//	            CmdSelector:           cobrax.AllowCmdsContaining("get", "list"),
//	            LocalFlagSelector:     cobrax.AllowFlags("namespace", "output"),
//	            InheritedFlagSelector: cobrax.NoFlags,
//	        },
//	        {
//	            CmdSelector:           cobrax.AllowCmds("mycli delete"),
//	            LocalFlagSelector:     cobrax.ExcludeFlags("all", "force"),
//	            InheritedFlagSelector: cobrax.NoFlags,
//	        },
//	    },
//	    SloggerOptions: &slog.HandlerOptions{Level: slog.LevelDebug},
//	}
package cobrax
