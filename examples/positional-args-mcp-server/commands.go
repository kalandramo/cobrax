// Package main demonstrates how to declare Cobra positional arguments so that
// cobrax can parse them into a well-structured MCP Tool JSON Schema, enabling
// LLMs to understand and supply each argument by name.
//
// # Positional Argument Syntax Rules
//
// cobrax reads the cmd.Use field and turns every bracket-enclosed token into a
// named property inside the "positional_args" object of the MCP JSON Schema.
// The token syntax follows the same convention used by kubectl, helm, and other
// popular CLIs:
//
//	<name>     – required, single value   → JSON Schema: string,  required
//	[name]     – optional, single value   → JSON Schema: string,  not required
//	<name...>  – required, variadic       → JSON Schema: string[], required
//	[name...]  – optional, variadic       → JSON Schema: string[], not required
//	[flags]    – cobra's flag sentinel    → silently ignored
//
// Use the cobrax.AnnotationArgPrefix+"<index>" annotation to supply a precise,
// human-readable description for each argument. Without annotations cobrax
// synthesises a generic description from the token shape.
//
// # Four Patterns Demonstrated
//
//  1. oncall  <module> [title]       – required + optional single-value args
//  2. cp      <src> <dst>            – two required single-value args
//  3. make    [targets...]           – optional variadic args
//  4. kubectl get <resource> [name]  – mixed required + optional in a sub-tree
package main

import (
	"fmt"

	"github.com/onexstack/cobrax"
	"github.com/spf13/cobra"
)

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 1 – required + optional single-value args
//
// cmd.Use:  "oncall <module> [title]"
//
// Resulting positional_args JSON Schema:
//
//	{
//	  "type": "object",
//	  "required": ["module"],
//	  "properties": {
//	    "module": { "type": "string", "description": "值班模块 ..." },
//	    "title":  { "type": "string", "description": "工单标题 ..." }
//	  }
//	}
//
// LLM input example:
//
//	{ "positional_args": { "module": "bke", "title": "节点 NotReady" } }
// ─────────────────────────────────────────────────────────────────────────────

func newOncallCmd() *cobra.Command {
	var level int

	cmd := &cobra.Command{
		Use: "oncall <module> [title]",

		Short: "提交值班工单",

		Long: `提交一个值班（on-call）工单，并将其路由到对应模块的值班人员。

位置参数说明：
  <module>  必填。值班模块名称，决定工单的路由目标。
  [title]   可选。工单标题；省略时系统自动生成默认标题。`,

		Example: `  # 提交工单到 bke 模块（使用默认标题）
  oncall bke

  # 提交工单到 kafka 模块并指定标题
  oncall kafka "消费者组 lag 持续增长"

  # 提交 P1 紧急工单
  oncall redis "主从切换失败" --level 1`,

		// Cobra 内置参数验证：至少 1 个，最多 2 个位置参数。
		// 与 cmd.Use 声明的参数数量保持一致。
		Args: cobra.RangeArgs(1, 2),

		// cobrax.AnnotationArgPrefix + "<index>" 为每个位置参数提供语义描述。
		// index 从 0 开始，与 cmd.Use 中 token 的出现顺序对应。
		Annotations: map[string]string{
			// 第 0 个 token：<module>
			cobrax.AnnotationArgPrefix + "0": "值班模块名称（如 bke、kafka、redis、mysql）。" +
				"决定工单路由到哪个团队的值班人员。",
			// 第 1 个 token：[title]
			cobrax.AnnotationArgPrefix + "1": "工单标题，简要描述问题现象（可选）。" +
				"省略时系统自动生成「[module] 告警」作为默认标题。",
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			module := args[0]
			title := fmt.Sprintf("[%s] 告警", module) // 默认标题
			if len(args) > 1 {
				title = args[1]
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"已提交工单：模块=%s 标题=%q 级别=P%d\n", module, title, level)
			return nil
		},
	}

	cmd.Flags().IntVar(&level, "level", 4, "工单级别 1-4（1 最紧急）")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 2 – two required single-value args
//
// cmd.Use:  "cp <src> <dst>"
//
// Resulting positional_args JSON Schema:
//
//	{
//	  "type": "object",
//	  "required": ["src", "dst"],
//	  "properties": {
//	    "src": { "type": "string", "description": "源文件路径 ..." },
//	    "dst": { "type": "string", "description": "目标路径 ..."  }
//	  }
//	}
//
// LLM input example:
//
//	{ "positional_args": { "src": "/tmp/dump.sql", "dst": "s3://bucket/backup/" } }
// ─────────────────────────────────────────────────────────────────────────────

func newCpCmd() *cobra.Command {
	var recursive bool

	cmd := &cobra.Command{
		Use:   "cp <src> <dst>",
		Short: "复制文件或目录",
		Long: `将源路径 <src> 的文件或目录复制到目标路径 <dst>。

两个位置参数均为必填项，缺少任意一个都会报错。`,
		Example: `  # 复制单个文件
  cp /var/log/app.log /backup/app.log

  # 递归复制目录
  cp --recursive /data/mysql/ /backup/mysql/`,

		Args: cobra.ExactArgs(2),

		Annotations: map[string]string{
			cobrax.AnnotationArgPrefix + "0": "源文件或目录的路径（必填）。支持绝对路径和相对路径。",
			cobrax.AnnotationArgPrefix + "1": "目标文件或目录的路径（必填）。目标目录须已存在。",
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			src, dst := args[0], args[1]
			flag := ""
			if recursive {
				flag = " -r"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cp%s %q → %q\n", flag, src, dst)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "递归复制目录")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 3 – optional variadic args
//
// cmd.Use:  "make [targets...]"
//
// Resulting positional_args JSON Schema:
//
//	{
//	  "type": "object",
//	  "properties": {
//	    "targets": {
//	      "type": "array",
//	      "items": { "type": "string" },
//	      "description": "要执行的 Make 目标列表 ..."
//	    }
//	  }
//	}
//
// LLM input example (multiple targets):
//
//	{ "positional_args": { "targets": ["build", "test", "lint"] } }
//
// LLM input example (no targets – run default):
//
//	{ "positional_args": {} }
// ─────────────────────────────────────────────────────────────────────────────

func newMakeCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "make [targets...]",
		Short: "执行 Makefile 目标",
		Long: `执行一个或多个 Makefile 目标。

[targets...] 为可选的变参列表：
  - 不提供时执行 Makefile 的默认目标（通常是第一个目标）。
  - 提供一个或多个目标时按顺序依次执行。`,
		Example: `  # 执行默认目标
  make

  # 执行单个目标
  make build

  # 执行多个目标
  make build test lint

  # 在指定目录执行
  make --dir /opt/app build`,

		Args: cobra.ArbitraryArgs,

		Annotations: map[string]string{
			cobrax.AnnotationArgPrefix + "0": "要执行的 Makefile 目标列表（可选）。" +
				"可以指定一个或多个目标，如 [\"build\", \"test\"]。" +
				"不提供时执行 Makefile 的默认目标。",
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"(default)"}
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"make dir=%q targets=%v\n", dir, args)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "在该目录下执行 make")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern 4 – sub-command tree with mixed required + optional args
//
// This demonstrates that each sub-command in a tree gets its own independent
// positional_args schema, derived from its own cmd.Use.
//
// kubectl get  <resource> [name]   – required resource type, optional name
// kubectl logs <pod>      [flags]  – required pod name only ([flags] ignored)
// kubectl exec <pod>      <cmd...> – required pod + required variadic command
// ─────────────────────────────────────────────────────────────────────────────

func newKubectlCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "kubectl",
		Short: "Kubernetes CLI（示例子命令树）",
	}
	root.AddCommand(newKubectlGetCmd(), newKubectlLogsCmd(), newKubectlExecCmd())
	return root
}

// kubectl get <resource> [name]
//
// Resulting positional_args JSON Schema:
//
//	{
//	  "required": ["resource"],
//	  "properties": {
//	    "resource": { "type": "string", "description": "资源类型 ..." },
//	    "name":     { "type": "string", "description": "资源名称 ..." }
//	  }
//	}
func newKubectlGetCmd() *cobra.Command {
	var namespace, output string

	cmd := &cobra.Command{
		Use:   "get <resource> [name]",
		Short: "查询 Kubernetes 资源",
		Long: `查询集群中的 Kubernetes 资源。

  <resource>  必填，资源类型（如 pod、deployment、service）。
  [name]      可选，资源名称；省略时列出该类型的所有资源。`,
		Example: `  # 列出所有 Pod
  kubectl get pod

  # 查询指定 Pod
  kubectl get pod my-app-7d4b9c8f6-xk2lp

  # 查询指定命名空间的 Deployment，以 YAML 格式输出
  kubectl get deployment my-app --namespace production --output yaml`,

		Args: cobra.RangeArgs(1, 2),

		Annotations: map[string]string{
			cobrax.AnnotationArgPrefix + "0": "Kubernetes 资源类型（必填）。" +
				"常见值：pod、deployment、service、configmap、secret、node。",
			cobrax.AnnotationArgPrefix + "1": "资源名称（可选）。" +
				"指定时查询单个资源详情；省略时列出该命名空间下所有同类资源。",
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			resource := args[0]
			name := ""
			if len(args) > 1 {
				name = args[1]
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"kubectl get resource=%q name=%q namespace=%q output=%q\n",
				resource, name, namespace, output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "目标命名空间")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "输出格式（table|json|yaml）")
	return cmd
}

// kubectl logs <pod> [flags]
//
// [flags] is the cobra sentinel and is silently ignored by cobrax.
// Resulting positional_args has only one property: "pod".
func newKubectlLogsCmd() *cobra.Command {
	var follow bool
	var tail int

	cmd := &cobra.Command{
		Use:   "logs <pod> [flags]",
		Short: "查看 Pod 日志",
		Example: `  # 查看 Pod 最近 100 行日志
  kubectl logs my-app-pod --tail 100

  # 持续输出日志（流式跟踪）
  kubectl logs my-app-pod --follow`,

		Args: cobra.ExactArgs(1),

		Annotations: map[string]string{
			cobrax.AnnotationArgPrefix + "0": "Pod 名称（必填）。" +
				"可通过 「kubectl get pod」 获取当前命名空间下的 Pod 列表。",
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"kubectl logs pod=%q follow=%v tail=%d\n", args[0], follow, tail)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "持续输出新日志（流式）")
	cmd.Flags().IntVar(&tail, "tail", -1, "显示最后 N 行（-1 表示全部）")
	return cmd
}

// kubectl exec <pod> <cmd...>
//
// Resulting positional_args JSON Schema:
//
//	{
//	  "required": ["pod", "cmd"],
//	  "properties": {
//	    "pod": { "type": "string",            "description": "Pod 名称 ..." },
//	    "cmd": { "type": "array",  items: string, "description": "在容器内执行的命令 ..." }
//	  }
//	}
func newKubectlExecCmd() *cobra.Command {
	var container string

	cmd := &cobra.Command{
		Use:   "exec <pod> <cmd...>",
		Short: "在 Pod 容器内执行命令",
		Long: `在指定 Pod 的容器内执行任意命令。

  <pod>     必填，目标 Pod 名称。
  <cmd...>  必填，要执行的命令及其参数（变参，至少一个）。`,
		Example: `  # 在 Pod 内执行 ls
  kubectl exec my-app-pod ls /app

  # 在 Pod 内运行 shell 命令
  kubectl exec my-app-pod sh -c "ps aux | grep app"

  # 指定容器
  kubectl exec my-app-pod --container sidecar cat /etc/config.yaml`,

		// 至少 2 个参数：pod 名 + 至少 1 个命令 token。
		Args: cobra.MinimumNArgs(2),

		Annotations: map[string]string{
			cobrax.AnnotationArgPrefix + "0": "目标 Pod 名称（必填）。",
			cobrax.AnnotationArgPrefix + "1": "在容器内执行的命令及其参数（必填，变参）。" +
				"例如 [\"ls\", \"-la\", \"/app\"] 或 [\"sh\", \"-c\", \"echo hello\"]。",
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			pod := args[0]
			command := args[1:]
			fmt.Fprintf(cmd.OutOrStdout(),
				"kubectl exec pod=%q container=%q cmd=%v\n", pod, container, command)
			return nil
		},
	}

	cmd.Flags().StringVarP(&container, "container", "c", "", "指定容器名称（Pod 内有多个容器时使用）")
	return cmd
}
