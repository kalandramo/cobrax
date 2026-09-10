# cobrax MCP 功能设计与实现分析

> cobrax（Ophis）将任意 Cobra CLI 自动转换为 MCP（Model Context Protocol）服务器，使 AI 助手（Claude Desktop、VSCode Copilot、Cursor 等）能够直接调用命令行工具。本文基于源码对其设计与实现进行完整分析。

## 1. 项目定位与总体架构

### 1.1 核心思想

```
Cobra 命令树 ──(注册期: 遍历+过滤+Schema生成)──> MCP Tool 集合
                                                     │
AI 客户端 ──(MCP: stdio/SSE/REST)──> 工具调用 ──(执行期: 参数还原)──> CLI 执行
```

| 问题 | 方案 |
|------|------|
| CLI 命令如何变成 LLM 可理解的工具 | 遍历命令树，把 flag/位置参数/描述转成 JSON Schema |
| LLM 的 JSON 参数如何变回 CLI 参数 | 扁平输入 + 注册期元数据 `toolMeta` 反切分 |
| 如何执行命令 | 双执行模型：子进程重入 / 进程内工厂 |
| 如何接入 AI 编辑器 | 内置 `mcp claude/vscode/cursor enable` 配置管理 |

### 1.2 依赖选型

- `github.com/mark3labs/mcp-go v0.30.0`：MCP 协议实现（Server、stdio/SSE 传输）
- `github.com/spf13/cobra v1.10.2` + `pflag`：CLI 框架（被转换对象）
- `github.com/google/jsonschema-go v0.4.2`：JSON Schema 构建与序列化

### 1.3 代码地图

```
cobrax/
├── doc.go                 # 包文档：双执行模型总述
├── root.go                # Command()：生成 "mcp" 管理命令树（入口）
├── config.go              # Config：子进程模型的服务器装配
├── server.go              # MCPOptions/MCPServer：进程内模型
├── selector.go            # Selector 体系 + createToolFromCmd（核心转换）
├── selectors.go           # 内置选择器（AllowCmds/ExcludeFlags/NoFlags...）
├── schema.go              # ToolInput/ToolOutput 数据结构
├── args.go                # 位置参数解析（cmd.Use token → ArgSpec）
├── annotations.go         # MCP 工具注解（readOnlyHint 等）
├── execute.go             # 子进程执行 + 扁平参数切分还原
├── start.go / stream.go   # mcp start（stdio）/ mcp stream（SSE）
├── restcmd.go / rest.go   # mcp rest（纯 REST 暴露）
├── tools.go               # mcp tools（导出工具清单 JSON）
├── internal/
│   ├── bridge/flags/      # pflag flag → JSON Schema 映射（flags.go/default.go）
│   ├── schema/            # JSON Schema 缓存工具
│   └── cfgmgr/            # 编辑器配置管理
│       ├── manager/       # 泛型 Manager + claude/cursor/vscode 配置实现
│       └── cmd/           # claude/cursor/vscode 的 enable/disable/list 命令
└── examples/
    ├── make/                       # 最小示例：make 包装器
    └── positional-args-mcp-server/ # 位置参数四种模式 + 进程内模型示例
```

## 2. 双执行模型

### 2.1 子进程模型（`Command` / `Config`）

`root.go:12` 的 `Command(config)` 向用户 CLI 注入 `mcp` 管理子命令树：

```go
cmd.AddCommand(
    startCommand(config),              // mcp start   → stdio MCP 服务器
    toolCommand(config),               // mcp tools   → 导出工具清单
    streamCommand(config),             // mcp stream  → SSE HTTP 服务器
    restCommand(config),               // mcp rest    → 纯 REST 服务器
    claude.Command(name, defaultEnv),  // mcp claude enable/disable/list
    vscode.Command(name, defaultEnv),  // mcp vscode enable/disable/list
    cursor.Command(name, defaultEnv),  // mcp cursor enable/disable/list
)
```

执行期每次工具调用**重新启动自身二进制**（`execute.go:37` `execSubprocess`）：启动时解析一次 `os.Executable()`（失败 panic），调用时 `exec.CommandContext(ctx, executablePath, args...)`。

- 适用：命令全部状态可由 CLI flag 重建（如 `make`、`kubectl` 包装器）。
- 优点：天然进程隔离、并发安全、崩溃不影响服务器。
- 代价：每次调用冷启动（重新加载配置、重建依赖）。

### 2.2 进程内模型（`NewMCPServer` / `MCPServer`）

`server.go:121` 的 `NewMCPServer(opts, cmdFactory)` 接收**命令树工厂函数**：

```go
factory := func() *cobra.Command {
    return buildRootCommand(biz)  // biz（API client、DB 句柄等）已注入闭包
}
srv, _ := cobrax.NewMCPServer(cobrax.MCPOptions{
    Enabled: true, Addr: ":8090", Name: "myapp", Version: "1.0.0",
}, factory)
```

工厂被调用两类次数（`server.go:108-160`）：

1. **注册期一次**：`schemaCmd := cmdFactory()` 做只读遍历生成工具 Schema，该树永不执行。
2. **每次工具调用一次**：`runInProcess` 内 `root := s.cmdFactory()` 产出全新命令树。

「每调用一工厂」设计（`server.go:304-343` 注释详述）消除一切共享可变状态：

- 每次调用的 Options 结构体、flag 值、ctx 字段全新分配 → 无 flag 串扰；
- `PersistentPreRunE` 的副作用（如 `--mock` 改写 RunE）限制在单次调用的树内；
- 无需 mutex、无需 reset 逻辑，并发调用完全独立。

两个细节：

- **panic 兜底**：`makeInProcessHandler` 用 `defer recover()` 捕获命令内 panic 转为工具错误。
- **上下文脱钩**（`server.go:372`）：`root.ExecuteContext(context.WithoutCancel(ctx))` —— AI 运行器收到工具结果后可能立即取消 context，`WithoutCancel` 保留全部 context 值（trace ID 等）但阻止取消传播，避免命令内已成功但在途的 I/O（如外部 API 调用）被误杀。

## 3. 命令 → 工具注册管道

两个模型共享同一条管道（`config.go:154` 与 `server.go:225` 各有一份 `registerToolsRecursive`，逻辑一致）：

```
递归遍历命令树（子命令先于父命令 → 工具列表 leaf-first）
    ↓
cmdFilter 基础安全过滤
    ↓
Selector 有序列表逐个匹配（first-match-wins）
    ↓
createToolFromCmd 生成 mcp.Tool + toolMeta
    ↓
server.AddTool(tool, handler)   ← handler 闭包捕获 selector 与 meta
```

### 3.1 基础安全过滤（不可绕过）

`cmdFilter`（`config.go:203`）无条件排除：

- `Hidden` 或 `Deprecated` 命令；
- 无 `Run/RunE/PreRun/PreRunE` 的不可执行命令（纯命令组）；
- 内置命令：cobrax 自身的 `mcp`（或 `Config.CommandName`）、`help`、`completion`。

### 3.2 Selector 体系（`selector.go`）

```go
type Selector struct {
    CmdSelector           CmdSelector    // 命令级匹配；nil = 匹配所有
    LocalFlagSelector     FlagSelector   // 本命令 flag 过滤；nil = 全包含
    InheritedFlagSelector FlagSelector   // 继承（persistent）flag 过滤
    Middleware            MiddlewareFunc // 执行中间件（可选）
}
```

语义：**选择器按序评估，第一个 `CmdSelector` 匹配的选择器胜出，其 FlagSelector 决定该命令暴露哪些 flag；无匹配则命令不成为工具。** `Selectors` 为空时注入全通配 `Selector{}`（默认暴露全部）。

内置选择器（`selectors.go`）：

| 类别 | 选择器 | 说明 |
|------|--------|------|
| 命令 | `AllowCmds(paths...)` | 精确匹配 CommandPath |
| 命令 | `AllowCmdsContaining(subs...)` | CommandPath 包含子串 |
| 命令 | `ExcludeCmds` / `ExcludeCmdsContaining` | 上两者的否定 |
| flag | `AllowFlags(names...)` / `ExcludeFlags(names...)` | 按 flag 名 |
| flag | `NoFlags` | 排除所有 flag |

典型用法（只读命令放行、危险命令收紧）：

```go
config := &cobrax.Config{
    Selectors: []cobrax.Selector{
        {
            CmdSelector:           cobrax.AllowCmdsContaining("get", "list"),
            LocalFlagSelector:     cobrax.AllowFlags("namespace", "output"),
            InheritedFlagSelector: cobrax.NoFlags,
        },
        {
            CmdSelector:           cobrax.AllowCmds("mycli delete"),
            LocalFlagSelector:     cobrax.ExcludeFlags("all", "force"),
            InheritedFlagSelector: cobrax.NoFlags,
        },
    },
}
```

### 3.3 Middleware

```go
type MiddlewareFunc func(ctx, req, input, next ExecuteFunc) (*mcp.CallToolResult, ToolOutput, error)
```

洋葱模型钩子，挂在选择器上（每个工具可不同），用于超时控制、响应过滤、指标采集、审计等。子进程模型在 `Selector.execute`（`selector.go:237`）包裹 `execute`；进程内模型在 `makeInProcessHandler` 包裹 `runInProcess`。两处均先 `recover` 再进中间件。

## 4. 扁平 JSON Schema 生成

### 4.1 扁平化设计（关键演进）

所有 flag 与位置参数平铺为顶层 `properties`，无嵌套 `flags`/`args` 子对象（`selector.go:86` `buildFlatSchema`）：

```json
{
  "type": "object",
  "properties": {
    "namespace": { "type": "string", "description": "目标命名空间", "default": "default" },
    "resource":  { "type": "string", "description": "Kubernetes 资源类型（必填）…" },
    "name":      { "type": "string", "description": "资源名称（可选）…" }
  },
  "required": ["resource"],
  "additionalProperties": { "not": {} }
}
```

`additionalProperties: not{}` 禁止客户端传入未声明字段。配套的注册期元数据（`selector.go:74`）：

```go
type toolMeta struct {
    flagNames map[string]struct{} // 工具 Schema 中的 flag 属性名集合
    argSpecs  []ArgSpec           // 有序位置参数规格（顺序 = cmd.Use 顺序）
}
```

执行期靠它把扁平 map 切分回 flag/位置参数（见 §5.2），这是「扁平 Schema 可逆」的关键。

### 4.2 flag → JSON Schema 映射（`internal/bridge/flags/flags.go`）

| pflag 类型 | JSON Schema |
|------------|-------------|
| `bool` | `boolean` |
| `int*` / `uint*` / `count` | `integer` |
| `float32/64` | `number` |
| `string` | `string`；若 flag 带 `jsonschema` 注解则整体替换为注解中的自定义 Schema |
| 各类 Slice（string/int/float/bool） | `array`（items 对应标量类型） |
| `stringToString` | `object`（additionalProperties: string） |
| `stringToInt(64)` | `object`（additionalProperties: integer） |
| `duration` | `string` + 正则 `^-?([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$` |
| `ip` / `ipNet` / `bytesHex` / `bytesBase64` | `string` + 对应 pattern |
| 未知类型 | `string` + description 标注 `(type: xxx)` |

要点：

- **required**：读取 cobra 的 `BashCompOneRequiredFlag` 注解（`MarkFlagRequired` 的落地处）→ Schema `required`。
- **default**：`default.go` 从 `flag.DefValue` 解析。pflag 数组默认值是 `"[a,b]"` 字符串，`parseArray/parseObject` 切分并类型转换（`"[]"` 视为空、非法元素跳过并 WARN）。
- **自定义 Schema 逃生舱**：string flag 可用 `jsonschema` 注解整体覆盖，例如把结构体 Schema（`jsonschema.For[T]`）注入，让 LLM 传完整 JSON 对象。

### 4.3 位置参数解析（`args.go`）

从 `cmd.Use` 的 token 语法推导位置参数：

| cmd.Use 写法 | 语义 | Schema |
|--------------|------|--------|
| `<module>` | 必填单值 | `string`，入 required |
| `[title]` | 可选单值 | `string` |
| `[targets...]` / `<files...>` | 变参 | `array of string` |
| `[flags]` | cobra 哨兵 | 静默跳过 |

解析正则（`args.go:68`）：`([<\[])([^>\]\s.]+)(\.{3})?[>\]]`，三组分别捕获括号类型（`<` 必填 / `[` 可选）、参数名、`...` 变参标记。参数名经 `normaliseName` 规范化（小写、非字母数字转下划线）。

**参数语义描述**（决定 LLM 填参质量）由注解提供，优先级高于自动合成：

```go
cmd.Annotations = map[string]string{
    cobrax.AnnotationArgPrefix + "0": "值班模块名称（如 bke、kafka、redis）…",
    cobrax.AnnotationArgPrefix + "1": "工单标题（可选），省略时自动生成…",
}
```

无注解时合成兜底描述，如 `"One or more targets values (optional). Usage: [targets...]"`。cobra 的 `Args` 校验器（`RangeArgs` 等）不参与 Schema 生成，由底层命令执行时自行校验。

### 4.4 工具名与描述（`selector.go:195`）

- **名称**：`CommandPath()` 中根命令名替换为 `ToolNamePrefix`（缩短长名以满足 Claude 64 字符限制），空格转下划线。如 `myapp sre open` → `myapp_sre_open`。进程内模型刻意用空前缀，工具名不含根命令名（根名可能含 MCP 工具名非法字符如 `/`）。
- **描述**：`Long` > `Short` > 兜底 `"Execute the X command"`，追加 `Example`。Long/Example 是向 LLM 传授用法的主要通道。

### 4.5 MCP 工具注解（`annotations.go`）

从 `cmd.Annotations` 读取 5 个键填充 `mcp.ToolAnnotation`：`title`（Title）、`readOnlyHint`（ReadOnlyHint，不修改环境）、`destructiveHint`（破坏性更新）、`idempotentHint`（幂等）、`openWorldHint`（与外部实体交互）。布尔经 `strconv.ParseBool` 解析（接受 "1"/"t"/"true" 等），非法值 WARN 跳过。

## 5. 工具执行流程

### 5.1 输入解码

`config.go:219` `decodeToolInput` 把 MCP 请求参数组装为贯穿全库的核心结构 `ToolInput`（`schema.go:11`）：

```go
input := ToolInput{
    FlatInput: req.GetArguments(),  // 原样扁平 map
    FlagNames: meta.flagNames,      // 注册期算好的 flag 名集合
    ArgNames:  argNames,            // 注册期算好的有序位置参数名
}
```

**输入永远扁平，分类信息来自注册期元数据。**

### 5.2 扁平参数 → CLI 参数还原（`execute.go`）

```
FlatInput
    ↓ splitFlatInput (execute.go:130)
    ├── FlagNames 命中的键 → flagMap
    └── ArgNames 按声明顺序取值 → posArgs（变参展开 []any/[]string，单值容忍为单元素）
    ↓ buildFlagArgs (execute.go:169)
    flagMap → CLI flag token 序列
```

flag 值 → CLI 参数规则：

| 输入值 | 生成的 CLI 参数 |
|--------|-----------------|
| `true` | `--flag` |
| `false` / `nil` | （省略） |
| 标量 | `--flag value` |
| `[a, b]` | `--flag a --flag b`（重复 flag，pflag slice 语义） |
| `{"k": v}` | `--flag k=v`（stringToString 语义） |

### 5.3 子进程模型执行

`execute.go:68` `execute` → `buildCommandArgs`：

1. `splitToolName("myapp_sub_command")` 按下划线切分，**丢弃首段（根名）**得命令路径 `["sub", "command"]`；
2. 追加 `buildFlagArgs` 与 `posArgs`；
3. `execSubprocess` 重入自身二进制，捕获 stdout/stderr/exitCode。

MCP 客户端取消请求 → context 取消 → 子进程被杀 → 返回错误。

### 5.4 进程内模型执行

`server.go:344` `runInProcess` 与子进程模型的关键差异：**命令路径不从工具名反推**。

```go
cmdPath := s.toolPaths[name]              // 注册期预存（cmdSubPath 剥离根命令名）
args := buildInProcessArgsFromPath(cmdPath, input)
root := s.cmdFactory()                    // 全新命令树
root.SetOut(&stdout); root.SetErr(&stderr); root.SetArgs(args)
execErr := root.ExecuteContext(context.WithoutCancel(ctx))
```

`toolPaths` 在注册期由 `cmdSubPath`（`server.go:400`）从 `CommandPath()` 计算（如 `"myapp sre open"` → `["sre", "open"]`），因为工具名可能因 `ToolNamePrefix` 改写或根名含非法字符而不可逆。子进程模型仍从工具名切分（`buildCommandArgs`）——这是两模型实现上的细微不对称。

RunE 错误处理：execErr → exitCode=1；若 stderr 为空则把错误信息写入 stderr（保证客户端可见）。

### 5.5 输出转换

`ToolOutput{StdOut, StdErr, ExitCode}`（`schema.go:27`）→ `mcp.CallToolResult`（`config.go:239`）：

- stdout 与 stderr 以换行拼接；
- `ExitCode != 0` → `NewToolResultError`（即使无输出也附 `"exit code N"`）；
- 否则 → `NewToolResultText`。

即**命令业务失败（非零退出）与执行失败（无法启动/panic）都归一化为 MCP 工具错误**，LLM 可读到完整输出。

## 6. 四种服务暴露形态

| 形态 | 命令/API | 传输 | 场景 |
|------|----------|------|------|
| stdio | `myapp mcp start`（`start.go`） | `mcpserver.ServeStdio` | Claude Desktop 等本地 MCP 客户端拉起的子进程 |
| SSE | `myapp mcp stream --host --port`（`stream.go`） | `mcpserver.NewSSEServer` | 远程 HTTP 访问 MCP |
| REST | `myapp mcp rest --port`（`restcmd.go`/`rest.go`） | 原生 `http.ServeMux` | 非 MCP 消费方（curl、CI、脚本） |
| 进程内 SSE | `NewMCPServer().Start()`（`server.go:179`） | SSE | 嵌入长驻应用 |

REST 形态（`rest.go`）值得注意：它**复用整套 MCP 管道**——同样的 `registerTools` 注册、同样的 `toolMeta`、同样的 `buildCommandArgs`/`execSubprocess` 执行路径，只是把 MCP 协议换成朴素 HTTP：

```
POST /myapp_oncall        # 路径 = MCP 工具名
{"module": "bke", "level": 3}
→ {"stdout": "...", "stderr": "...", "exitCode": 0}
```

错误码约定：400（JSON 解析失败）、404（未知工具）、405（非 POST）、500（子进程启动失败）。这让同一 CLI 同时服务 AI 与传统自动化。空请求体视为零参数。

SSE/REST 服务器都监听 cobra 命令 context 取消做优雅停机；进程内模型额外监听 SIGINT/SIGTERM。

## 7. 编辑器配置管理（internal/cfgmgr）

### 7.1 泛型 Manager

`manager/manager.go:32` 用泛型统一三种编辑器的配置文件操作：

```go
type Manager[S Server, C Config[S]] struct { configPath string; config C }
// C 实现 HasServer/AddServer/RemoveServer/Print
```

三个构造器 `NewClaudeManager / NewVSCodeManager / NewCursorManager` 各自绑定目标平台（默认路径均含 darwin/linux/windows 平台特定文件，用 build tag 区分）：

| 编辑器 | 默认路径 | 配置形态 |
|--------|----------|----------|
| Claude Desktop | 如 `~/Library/Application Support/Claude/claude_desktop_config.json` | `{"mcpServers": {name: {command, args, env}}}` |
| VSCode | `.vscode/mcp.json`（workspace）或用户级 `mcp.json` | 支持 stdio/http 类型（`Type`/`URL`/`Headers`） |
| Cursor | `.cursor/mcp.json` 或用户级 | 同 Claude 结构 |

安全机制：每次写入前把现有文件备份为 `<name>.backup.json`（`manager.go:163`）；目标目录自动 `MkdirAll`；文件不存在时按空配置初始化。

### 7.2 enable 的装配逻辑（`cmd/claude/enable.go`）

`myapp mcp claude enable` 写入的条目：

```json
"myapp": {
  "command": "/path/to/myapp",
  "args": ["mcp", "start", "--log-level", "debug"],
  "env": {"PATH": "..."}
}
```

- **command** = `os.Executable()` 当前二进制绝对路径；
- **args** = `GetCmdPath(cmd, "mcp")` 的结果 + `"start"` —— `GetCmdPath`（`manager/utils.go:31`）从**当前命令路径**中定位 cobrax 命令段，使 cobrax 命令挂在任意子命令层级下仍能正确重建启动路径；
- **env** = `Config.DefaultEnv`（典型用法：捕获 PATH 让 MCP 子进程找到非系统路径下的可执行文件）合并用户 `--env`，**用户值优先**；
- **服务器名**默认 `DeriveServerName`（可执行文件 basename 去扩展名），可用 `--server-name` 覆盖。

disable 与 list 是对称的配置读写。编辑器侧的重启与信任确认仍需用户操作。

## 8. 关键设计决策与权衡

1. **双模型而非单模型**：进程内模型为「命令闭包持有不可序列化依赖」的现实而生（doc.go 明示这是 preferred model），子进程模型保留是因为它对纯 flag 驱动的 CLI 零侵入且天然隔离。二者共享 Schema 生成与参数还原代码，仅执行末端不同。

2. **工厂模式消灭并发问题**：进程内模型不用「单树 + reset + mutex」，而是每调用造新树。代价是每次调用的分配开销，换来正确性上的简单——所有状态都是调用私有的。

3. **扁平 Schema + 注册期元数据**：放弃嵌套 `flags`/`args` 对象，让 LLM 少一层嵌套、少一类出错；代价是执行期必须依赖 `toolMeta` 反切分，因此 Schema 与元数据必须同源生成（`buildFlatSchema` 同时产出两者）。

4. **first-match-wins 选择器**：多选择器不叠加而是一次定胜负，一个命令的 flag 暴露策略由唯一选择器决定，语义可预测、便于调试。

5. **安全过滤与选择器分层**：hidden/deprecated/内置命令过滤在选择器之前且不可绕过（`CmdSelector` 无法复活 hidden 命令），选择器只能在安全边界内做细粒度收紧。

6. **工具名可逆性问题的两种解法**：子进程模型从工具名切分回命令路径（名字未被改写时可行）；进程内模型注册期预存 `toolPaths`（容忍 `ToolNamePrefix` 与非法字符）。后者更稳健。

7. **stdout 让给协议、日志走 stderr**：stdio 传输下 stdout 被 MCP JSON-RPC 帧占用，所有 slog 输出定向 stderr（`config.go:118`），并接受 `--log-level` 覆盖。

8. **`WithoutCancel` 的取消脱钩**：对「AI 运行器收到首个结果就 cancel context」的行为模式做防御，牺牲严格取消传播换取在途副作用（外部 API 调用）的完整性。

## 9. 快速上手

### 子进程模型（3 行接入）

```go
rootCmd := createMyRootCommand()
rootCmd.AddCommand(cobrax.Command(nil))   // 注入 mcp start/stream/tools/rest + 编辑器管理
err := rootCmd.Execute()
```

```bash
myapp mcp tools                    # 检查生成的工具清单 → mcp-tools.json
myapp mcp claude enable            # 注册进 Claude Desktop（vscode/cursor 同理）
myapp mcp start                    # stdio 服务器（编辑器拉起）
myapp mcp stream --port 8080       # 或 SSE 远程暴露
```

### 进程内模型

```go
biz := newBiz(apiClient, dbClient)                    // 依赖构造一次，只读共享
factory := func() *cobra.Command { return buildRootCmd(biz) }
srv, _ := cobrax.NewMCPServer(cobrax.MCPOptions{
    Enabled: true, Addr: ":8090", Name: "myapp", Version: "1.0.0",
}, factory, cobrax.WithSelectors(mySelectors...))
srv.Start(ctx)
```

### 提升工具质量的三板斧

1. `cmd.Long` + `cmd.Example` 写清用途（LLM 的主要信息源）；
2. `cobrax.AnnotationArgPrefix + "<index>"` 为每个位置参数写语义描述；
3. `cmd.Annotations` 声明 `readOnlyHint`/`destructiveHint` 等行为特征。

## 10. 现状与注意点

- `docs/execution.md`、`docs/schema.md` 已按当前扁平 Schema 实现重写（2026-09-10），与本文一致。
- `examples/positional-args-mcp-server/` 是位置参数四种模式（必填+可选、双必填、变参、子命令树）与进程内模型的活参考。
- 进程内模型的 `MCPServer.Start` 仅支持 SSE（`server.go:199`），stdio 仅供子进程模型（`Config.serveStdio`）。
- README 中间件签名已对齐代码（`next cobrax.ExecuteFunc`）。
