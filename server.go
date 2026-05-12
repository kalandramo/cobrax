package cobrax

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// MCPOptions holds configuration for the MCP (Model Context Protocol) server.
// It is used with NewMCPServer to control how the server advertises itself and
// which network address it listens on.
type MCPOptions struct {
	// Enabled controls whether the MCP server is started.
	// When false, Start returns immediately without binding any port.
	Enabled bool `json:"enabled" mapstructure:"enabled"`

	// Addr is the listen address for the MCP server (e.g. ":8090").
	// Required when Enabled is true.
	Addr string `json:"addr" mapstructure:"addr"`

	// Name is the MCP server name advertised to clients.
	Name string `json:"name" mapstructure:"name"`

	// Version is the MCP server version advertised to clients.
	Version string `json:"version" mapstructure:"version"`

	// BaseURL is the publicly reachable base URL for the SSE server
	// (e.g. "http://localhost:8090"). Used to construct the SSE endpoint
	// advertised to clients. If empty, it is derived from Addr.
	BaseURL string `json:"baseURL" mapstructure:"baseURL"`
}

// MCPServer is an MCP server that exposes Cobra commands as MCP tools.
//
// Unlike the subprocess-based execution model used by [Config], MCPServer runs
// every tool handler in-process by directly invoking the matched cobra.Command.
// This means all dependencies captured in command closures (database clients,
// API clients, business-logic objects, etc.) are available at call time without
// any extra wiring — the caller simply constructs the cobra.Command tree with
// its dependencies already injected before passing it to [NewMCPServer].
//
// Example:
//
//	biz := newBotSreBiz(client, clientset, ...)
//	root := cobra.Command{Use: "myapp"}
//	root.AddCommand(sre.NewOpenCmd(biz, ioStreams))
//
//	srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{
//	    Enabled: true,
//	    Addr:    ":8090",
//	    Name:    "myapp",
//	    Version: "1.0.0",
//	}, &root)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := srv.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
type MCPServer struct {
	opts    MCPOptions
	server  *mcpserver.MCPServer
	rootCmd *cobra.Command
	tools   []*mcp.Tool
	// selectors holds the set of selector rules used during tool registration.
	// Defaults to a single catch-all Selector{} when none are configured.
	selectors []Selector
	// toolArgSpecs maps tool name → ordered ArgSpec slice for that tool's
	// positional arguments. This allows the in-process execution path to
	// reassemble PositionalArgs in the correct CLI order.
	toolArgSpecs map[string][]ArgSpec
	// toolPaths maps tool name → the cobra sub-command path segments to pass
	// to rootCmd (i.e. the command path with the root name stripped). This is
	// stored at registration time so that runInProcess never needs to reverse-
	// engineer the path from the tool name, which breaks when rootCmd.Name()
	// contains characters that are illegal in MCP tool names (e.g. "/").
	toolPaths map[string][]string
}

// ServerOption is a functional option for configuring the MCPServer.
type ServerOption func(*MCPServer)

// WithSelectors sets the selector rules used during tool registration.
// If provided, it overrides the default catch-all Selector{}.
func WithSelectors(selectors ...Selector) ServerOption {
	return func(s *MCPServer) {
		if len(selectors) > 0 {
			s.selectors = selectors
		}
	}
}

// NewMCPServer constructs an MCPServer by converting every runnable command in
// rootCommand's tree into an MCP tool.
//
// The rootCommand and all its subcommands are walked recursively. For each
// command that passes the built-in safety filters (not hidden, not deprecated,
// has a Run/RunE function, is not the built-in help/completion group) a
// corresponding MCP tool is registered on the returned server.
//
// Each tool's handler runs the matched cobra.Command in-process, which means
// any non-cobra dependencies already captured in the command's closure are
// available at invocation time. stdout and stderr from the command are captured
// and returned in the ToolOutput.
//
// opts.Enabled must be true for Start to actually bind a port; if false, Start
// is a no-op, which lets callers gate the MCP server behind a feature flag.
func NewMCPServer(opts MCPOptions, rootCommand *cobra.Command, serverOpts ...ServerOption) (*MCPServer, error) {
	if rootCommand == nil {
		return nil, fmt.Errorf("rootCommand must not be nil")
	}

	// Resolve server name: prefer opts.Name, fall back to rootCommand.Name().
	name := opts.Name
	if name == "" {
		name = rootCommand.Name()
	}

	version := opts.Version

	mcpSrv := mcpserver.NewMCPServer(name, version)

	srv := &MCPServer{
		opts:         opts,
		server:       mcpSrv,
		rootCmd:      rootCommand,
		selectors:    []Selector{{}}, // default catch-all selector
		toolArgSpecs: make(map[string][]ArgSpec),
		toolPaths:    make(map[string][]string),
	}

	for _, opt := range serverOpts {
		opt(srv)
	}
	// Register all commands from the tree as MCP tools.
	srv.registerToolsRecursive(rootCommand)

	return srv, nil
}

// Tools returns the list of MCP tools registered on this server.
// The slice is ordered leaf-first (depth-first traversal order).
// It can be used for introspection, testing, or generating tool manifests.
func (s *MCPServer) Tools() []*mcp.Tool {
	return s.tools
}

// RawMCPServer returns the underlying mcpserver.MCPServer instance.
func (s *MCPServer) RawMCPServer() *mcpserver.MCPServer {
	return s.server
}

// Start begins serving the MCP server over SSE on opts.Addr.
//
// It blocks until the context is cancelled or an OS signal (SIGINT/SIGTERM) is
// received, at which point it performs a graceful shutdown. If opts.Enabled is
// false, Start returns nil immediately.
func (s *MCPServer) Start(ctx context.Context) error {
	if !s.opts.Enabled {
		slog.Info("MCP server is disabled, skipping start")
		return nil
	}

	if s.opts.Addr == "" {
		return fmt.Errorf("MCPOptions.Addr must not be empty when Enabled is true")
	}

	// Derive the base URL for the SSE server.
	baseURL := s.opts.BaseURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://%s", s.opts.Addr)
		// Normalize addr like ":8090" to "localhost:8090" for the URL.
		if len(s.opts.Addr) > 0 && s.opts.Addr[0] == ':' {
			baseURL = fmt.Sprintf("http://localhost%s", s.opts.Addr)
		}
	}

	sseServer := mcpserver.NewSSEServer(s.server)

	// Watch for cancellation or OS signals to trigger graceful shutdown.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
		case <-ctx.Done():
		}
		signal.Stop(ch)

		if err := sseServer.Shutdown(context.Background()); err != nil {
			slog.Error("error shutting down MCP SSE server", "error", err)
		}
	}()

	slog.Info("MCP SSE server listening", "addr", s.opts.Addr, "baseURL", baseURL)
	if err := sseServer.Start(s.opts.Addr); err != nil {
		return fmt.Errorf("MCP SSE server error: %w", err)
	}
	return nil
}

// registerToolsRecursive walks the command tree rooted at cmd and registers
// every eligible command as an MCP tool on s.server.
func (s *MCPServer) registerToolsRecursive(cmd *cobra.Command) {
	// Recurse into sub-commands first so the tool list is ordered leaf-first.
	for _, sub := range cmd.Commands() {
		s.registerToolsRecursive(sub)
	}

	// Apply built-in safety filters.
	if s.cmdFilter(cmd) {
		return
	}

	// Build the cobra sub-command path for this command: the full CommandPath()
	// minus the root command name. This is stored in toolPaths so that
	// runInProcess can pass the exact segments to rootCmd without having to
	// reverse-engineer them from the tool name (which would break when
	// rootCmd.Name() contains characters illegal in MCP tool names, e.g. "/").
	cmdPath := cmdSubPath(cmd)

	// Use an empty tool name prefix so the tool name is derived purely from the
	// sub-command path. The root command name is intentionally excluded because
	// it may contain characters that are illegal in MCP tool names (e.g. "/").
	toolNamePrefix := ""

	// Evaluate selectors in order; the first matching selector wins.
	for i, sel := range s.selectors {
		if sel.CmdSelector != nil && !sel.CmdSelector(cmd) {
			continue
		}

		// Parse the positional argument specs for this command so they can be
		// stored alongside the tool and used during in-process execution.
		specs := parseArgSpecs(cmd)

		// Create the MCP tool definition (schema, name, description).
		tool := sel.createToolFromCmd(cmd, toolNamePrefix)
		slog.Debug("registered in-process tool", "tool_name", tool.Name, "selector_index", i)

		// Store the ArgSpec slice keyed by tool name for use at call time.
		if len(specs) > 0 {
			s.toolArgSpecs[tool.Name] = specs
		}

		// Store the cobra command path segments for this tool.
		s.toolPaths[tool.Name] = cmdPath
		fmt.Println("22222222222222222222222222222222222222222222", cmd.CommandPath(), "|", tool.Name, "|", cmdPath)

		// Capture sel and cmd in a closure for the tool handler.
		handler := s.makeInProcessHandler(sel, cmd)
		s.server.AddTool(*tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			input := decodeToolInput(req)
			result, output, err := handler(ctx, req, input)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if result != nil {
				return result, nil
			}
			return toolOutputToResult(output), nil
		})

		s.tools = append(s.tools, tool)

		// Only the first matching selector is used.
		break
	}
}

// cmdFilter returns true when cmd should be excluded from tool registration.
//
// Commands are excluded when they are hidden, deprecated, lack a runnable
// function, or belong to the built-in help/completion groups.
func (s *MCPServer) cmdFilter(cmd *cobra.Command) bool {
	if cmd.Hidden || cmd.Deprecated != "" {
		return true
	}

	if cmd.Run == nil && cmd.RunE == nil && cmd.PreRun == nil && cmd.PreRunE == nil {
		return true
	}

	// Exclude built-in utility commands.
	return AllowCmdsContaining("help", "completion")(cmd)
}

// makeInProcessHandler returns a tool handler function that executes cmd
// in-process rather than as a subprocess.
//
// The handler:
//  1. Builds the CLI argument list from the MCP tool input (flags + positional
//     args), the same way buildCommandArgs does for subprocess execution.
//  2. Re-uses the original cobra.Command tree already populated with the
//     caller's dependencies by creating a fresh argument set on rootCmd and
//     calling ExecuteContext. stdout and stderr are redirected to buffers so
//     they can be returned in the ToolOutput.
//  3. Captures the exit code from cobra's error (RunE returning a non-nil error
//     is treated as exit code 1) and wraps any panic in a Go error.
//
// Because the cobra.Command's RunE/Run closures already hold references to the
// caller's runtime objects (e.g. *botSreBiz, API clients), those objects are
// fully available without any additional wiring.
func (s *MCPServer) makeInProcessHandler(sel Selector, _ *cobra.Command) func(context.Context, mcp.CallToolRequest, ToolInput) (*mcp.CallToolResult, ToolOutput, error) {
	return func(ctx context.Context, req mcp.CallToolRequest, input ToolInput) (result *mcp.CallToolResult, output ToolOutput, err error) {
		// Recover from any panic inside the command.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic during tool execution: %v", r)
			}
		}()

		// Apply optional middleware.
		if sel.Middleware != nil {
			executeFunc := func(ctx context.Context, req mcp.CallToolRequest, input ToolInput) (*mcp.CallToolResult, ToolOutput, error) {
				return s.runInProcess(ctx, req, input)
			}
			return sel.Middleware(ctx, req, input, executeFunc)
		}

		return s.runInProcess(ctx, req, input)
	}
}

// runInProcess executes the cobra command in-process by setting the argument
// list on rootCmd and calling ExecuteContext.
//
// stdout and stderr are captured via bytes.Buffer by temporarily replacing the
// root command's output writers. The original writers are restored after the
// call to avoid leaking state between tool invocations.
func (s *MCPServer) runInProcess(ctx context.Context, req mcp.CallToolRequest, input ToolInput) (*mcp.CallToolResult, ToolOutput, error) {
	name := req.Params.Name
	slog.Info("in-process MCP tool request", "tool", name)

	// Look up the ArgSpec slice for this tool (may be nil for commands without
	// named positional argument tokens).
	specs := s.toolArgSpecs[name]

	// Retrieve the pre-computed cobra sub-command path for this tool.
	// This avoids having to reverse-engineer the path from the tool name,
	// which breaks when rootCmd.Name() contains characters that are illegal
	// in MCP tool names (e.g. "/").
	cmdPath := s.toolPaths[name]

	// Build CLI args from the stored command path and the tool input.
	args := buildInProcessArgsFromPath(cmdPath, input, specs)
	slog.Info("in-process command args", "tool", name, "args", args)

	// Capture output by replacing the root command's output writers.
	var stdout, stderr bytes.Buffer
	s.rootCmd.SetOut(&stdout)
	s.rootCmd.SetErr(&stderr)

	// Set the args on the root command and execute.
	s.rootCmd.SetArgs(args)
	exitCode := 0
	if execErr := s.rootCmd.ExecuteContext(ctx); execErr != nil {
		// cobra surfaces RunE errors here; treat them as exit code 1.
		slog.Warn("in-process command returned error", "tool", name, "error", execErr)
		exitCode = 1
		// Write the error message to stderr so the MCP client can see it.
		if stderr.Len() == 0 {
			stderr.WriteString(execErr.Error())
		}
	}

	return nil, ToolOutput{
		StdOut:   stdout.String(),
		StdErr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// cmdSubPath returns the cobra sub-command path segments for cmd, i.e. the
// full CommandPath() with the root command name stripped. For example, if the
// full path is "myapp sre open", the result is ["sre", "open"]. If cmd is the
// root command itself, the result is nil.
//
// The segments are computed at tool-registration time and stored in
// MCPServer.toolPaths so that runInProcess never needs to reverse-engineer
// the path from the tool name.
func cmdSubPath(cmd *cobra.Command) []string {
	path := cmd.CommandPath() // e.g. "myapp sre open"
	spaceIdx := strings.IndexByte(path, ' ')
	if spaceIdx == -1 {
		// Root command itself — no sub-path.
		return nil
	}
	rest := path[spaceIdx+1:] // "sre open"
	return strings.Split(rest, " ")
}

// buildInProcessArgsFromPath constructs the cobra argument slice from the
// pre-computed command sub-path, the tool input, and the ordered ArgSpec slice.
//
// cmdPath contains the cobra sub-command segments (root already stripped), e.g.
// ["sre", "open"]. Flags and positional arguments are appended after them.
func buildInProcessArgsFromPath(cmdPath []string, input ToolInput, specs []ArgSpec) []string {
	// Start with a copy of the command path segments.
	parts := make([]string, len(cmdPath))
	copy(parts, cmdPath)

	// Append flag arguments.
	parts = append(parts, buildFlagArgs(input.Flags)...)

	// Append positional arguments in the correct order.
	if len(specs) > 0 && len(input.PositionalArgs) > 0 {
		// Use spec-ordered collection for named positional args.
		parts = append(parts, collectPositionalArgs(specs, input.PositionalArgs)...)
	} else {
		// Fall back to the legacy flat args slice.
		parts = append(parts, input.Args...)
	}

	return parts
}
