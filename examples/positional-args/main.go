package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/onexstack/cobrax"
	"github.com/spf13/cobra"
)

// main demonstrates two ways to serve positional-arg commands via MCP:
//
//   - Subprocess model: attach the "mcp" management sub-command tree to the
//     root command, then run "myops mcp start" to launch a stdio MCP server.
//     The server re-invokes the same binary for every tool call.
//
//   - In-process model: construct an MCPServer directly and call Start.
//     Tool handlers run inside the same process without subprocess overhead.
//
// This binary defaults to the subprocess model so it can be explored through
// the standard "mcp tools" / "mcp start" workflow.  Switch to in-process by
// passing --in-process.
func main() {
	var inProcess bool
	var addr string

	rootCmd := buildRootCmd()

	// Mode selector — not a real production pattern; kept intentionally simple
	// to keep the example focused on positional-arg declaration.
	modeCmd := &cobra.Command{
		Use:   "serve",
		Short: "直接启动 MCP HTTP 服务（in-process 模式）",
		Long: `以 in-process 模式启动 MCP streamable-HTTP 服务器。

工具处理器直接在同一进程中运行对应的 cobra.Command，无需子进程开销。
适合命令依赖进程内状态（如数据库连接、缓存对象）的场景。`,
		Example: `  # 在 :8090 启动 MCP 服务
  myops serve --addr :8090`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = inProcess // illustrative; always in-process here
			srv, err := cobrax.NewMCPServer(cobrax.MCPOptions{
				Enabled: true,
				Addr:    addr,
				Name:    "myops",
				Version: "0.1.0",
			}, buildRootCmd)
			if err != nil {
				return fmt.Errorf("create MCP server: %w", err)
			}
			log.Printf("MCP server listening on %s", addr)
			return srv.Start(context.Background())
		},
	}
	modeCmd.Flags().BoolVar(&inProcess, "in-process", true, "使用 in-process 执行模式（当前示例固定为 in-process）")
	modeCmd.Flags().StringVar(&addr, "addr", ":8090", "MCP HTTP 服务监听地址")

	rootCmd.AddCommand(modeCmd)
	// Attach the standard "mcp start / mcp tools / mcp claude ..." sub-tree
	// for the subprocess model.
	rootCmd.AddCommand(cobrax.Command(nil))

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// buildRootCmd constructs the full command tree that will be exposed as MCP
// tools.  Each leaf command demonstrates a different positional-arg pattern.
func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "myops",
		Short: "运维助手 CLI（positional-args 示例）",
		Long: `myops 是一个演示 cobrax 位置参数功能的运维助手 CLI。

它包含四个命令，覆盖 cobrax 支持的全部位置参数模式：

  oncall  <module> [title]     – 必填 + 可选 单值参数
  cp      <src> <dst>          – 两个必填单值参数
  make    [targets...]         – 可选变参列表
  kubectl get|logs|exec ...    – 子命令树，每个子命令独立声明参数

通过 "myops mcp tools" 可以查看生成的 MCP Tool JSON Schema，
验证每个位置参数都被映射为 positional_args 对象中的具名字段。`,
	}

	// Pattern 1: required + optional
	root.AddCommand(newOncallCmd())
	// Pattern 2: two required
	root.AddCommand(newCpCmd())
	// Pattern 3: optional variadic
	root.AddCommand(newMakeCmd())
	// Pattern 4: sub-command tree
	root.AddCommand(newKubectlCmd())

	return root
}
