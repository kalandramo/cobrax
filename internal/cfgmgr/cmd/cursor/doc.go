// Package cursor provides CLI commands for managing Cursor MCP servers.
//
// This package implements the 'mcp cursor' subcommands:
//   - enable: Add MCP server to Cursor configuration
//   - disable: Remove MCP server from Cursor configuration
//   - list: Show all configured MCP servers
//
// Supports both workspace (.cursor/mcp.json) and user-level configurations.
//
// This is an internal package and should not be imported directly by users of the cobrax library.
// These commands are automatically available when using cobrax.Command() in your application.
package cursor
