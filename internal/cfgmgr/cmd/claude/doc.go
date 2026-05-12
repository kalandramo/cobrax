// Package claude provides CLI commands for managing Claude Desktop MCP servers.
//
// This package implements the 'mcp claude' subcommands:
//   - enable: Add MCP server to Claude Desktop configuration
//   - disable: Remove MCP server from Claude Desktop configuration
//   - list: Show all configured MCP servers
//
// This is an internal package and should not be imported directly by users of the cobrax library.
// These commands are automatically available when using cobrax.Command() in your application.
package claude
