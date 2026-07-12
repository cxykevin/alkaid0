<p align="center">
  <picture>
    <source srcset="https://raw.githubusercontent.com/cxykevin/alkaid0/refs/heads/main/logo/wide160x40d.svg" media="(prefers-color-scheme: dark)">
    <img src="https://raw.githubusercontent.com/cxykevin/alkaid0/refs/heads/main/logo/wide160x40l.svg" alt="alkaid0-logo">
  </picture>
</p>

[![GitHub Repo stars](https://img.shields.io/github/stars/cxykevin/alkaid0?style=flat&link=https%3A%2F%2Fgithub.com%2Fcxykevin%2Falkaid0)](https://github.com/cxykevin/alkaid0)
[![GitHub Release](https://img.shields.io/github/v/release/cxykevin/alkaid0?include_prereleases&sort=semver&display_name=tag&style=flat)](https://github.com/cxykevin/alkaid0/releases)
[![GitHub License](https://img.shields.io/github/license/cxykevin/alkaid0?style=flat&cacheSeconds=100000&link=https%3A%2F%2Fgithub.com%2Fcxykevin%2Falkaid0)](https://github.com/cxykevin/alkaid0?tab=GPL-3.0-1-ov-file)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/cxykevin/alkaid0)](https://pkg.go.dev/github.com/cxykevin/alkaid0)
[![Build and Package](https://github.com/cxykevin/alkaid0/actions/workflows/build.yml/badge.svg)](https://github.com/cxykevin/alkaid0/actions/workflows/build.yml)

# Alkaid0

Alkaid0 是一个模块化的 AI Coding 工具 与 Agent 框架，专为构建具备多 Agent 能力、工具调用系统和流式响应处理功能的智能编码助手而设计。该框架基于 Go 语言构建。

设计理念：**低消耗** **用户友好** **可扩展** **强兼容性**

---

## 核心特性

- **多 Agent 架构**：支持主 Agent 与子 Agent（Subagent）嵌套调用，子 Agent 可独立绑定路径和作用域
- **沙箱安全执行**：内置沙箱隔离机制，Linux 使用 mount namespace，Windows 使用 DACL + 受限令牌
- **会话管理**：支持会话断连延迟释放（保留上下文，断线可重连恢复）、后台运行模式
- **自动审批规则**：基于 expr 表达式引擎，支持细粒度工具调用自动审批/拒绝
- **测试覆盖**：持续提升测试覆盖率，集成 mock 服务器测试

---

## 安装

```bash
# Linux
curl -sSL https://alk.cxykevin.top/i.sh | bash
```

```powershell
# Windows
irm https://alk.cxykevin.top/i.ps1 | iex
```
---

## 日志

路径 `~/.config/alkaid0/log.log` (直接启动二进制文件) / `/var/log/alkaid0/log.log` (Linux 系统级别安装) / `C:\ProgramData\alkaid0\config.json` (Windows 系统级别安装)

> 日志经过脱敏处理（脱去 Provider URL / KEY），但会保留请求的 Model ID 和 Agent Name 以及完整输入输出。日志不会携带工作区信息但 AI 模型的输出可能会包含部分用户代码。

提供 toolkit 包以查看日志。用法见其 `--help`。

---

## 配置

路径

```bash
# 从二进制文件启动
~/.config/alkaid0/config.json
# Linux 软件包安装版本
/etc/alkaid0/config.json
# Windows 软件包安装版本
C:\ProgramData\alkaid0\config.json
```

```json
{
    "$schema": "https://raw.githubusercontent.com/cxykevin/alkaid0/refs/heads/main/docs/schemas/config.json",
    "Version": 1,
    "Model": {
        "ProviderURL": "https://openrouter.com/api/v1（这里暂时没有用）",
        "ProviderKey": "sk-or-xxx（这里暂时没有用）",
        "DefaultModelID": 1,
        "Models": {
            "1": {
                "ModelName": "模型名",
                "ModelID": "模型ID",
                "ProviderURL": "https://模型供应商/v1",
                "ProviderKey": "sk-模型密钥",
                "EnableThinking": true, 
                "CompressSize": 128000,
                "ProviderSpecificConfig": {
                    "EnableDeepseekThinking": false,
                    "EnableReasoningEffort": true,
                    "EnableTopP": false,
                    "EnableTopK": false,
                    "EnableTemperature": false
                }
            }
        }
    },
    "Agent": {
        "Agents": {
            "frontend": {
                "AgentName": "前端工程",
                "AgentDescription": "前端工程Agent",
                "AgentPrompt": "你是一个前端工程师，请根据用户的需求，提供前端工程解决方案。",
                "AgentModel": 1,
                "AutoApprove": "",
                "AutoReject": ""
            }
        },
        "GlobalPrompt": "始终使用中文回答",
        "SummaryModel": 1,
        "MaxCallCount": 50,
        "AutoApprove": "",
        "AutoReject": ""
    },
    "ThemeID": 0,
    "Server": {
        "Key": "<你的 webcsocket key>",
        "Path": "/acp",
        "Host": "127.0.0.1",
        "Port": 7433,
        "DisableStdioServer": false
    }
}
```

### 远程配置 RPC

支持通过 RPC 方法 `alk.cxykevin.top/config/get` 和 `alk.cxykevin.top/config/set` 远程读取和修改配置，方便客户端集成。

---

## 客户端

配置完上述 json 后直接启动主程序，服务端会在 `ws://<host>:<port>/<path>` 开启一个 websocket 服务。此时服务端同时会启动一个标准的 stdio 服务器便于调试。

服务端使用 Query 参数认证。在 Query 参数中添加 `key=<key>` 即可。如果没有 Query 参数选项，则可以在 `Path` 中设置 `/acp?k=<key>`。

支持 Websocket 桥接的客户端可以直接链接。只支持 stdio 的客户端可以使用提供的 helper 链接。

> 目前 helper 只支持 `ws`，不支持 `wss`。

可以通过 `./可执行文件 acp` 启动服务端内置 helper。如果你需要轻量化部署并链接到远程，则可以使用单独的 helper 可执行文件并不带任何参数启动。如果你使用 `go install` 安装，则服务器绑定到了 `alkaid0`，单独的 helper 可以使用 `alk` 命令。

**helper 会自动读取本机的 `~/.config/alkaid0/config.json` 并自动链接，一般本机无需再次手动配置 key。**

如果你需要链接到远程或自动链接无效，可以使用以下参数：

- `-config` 配置文件路径
- `-host` 服务器的 host
- `-port` 服务器的 port
- `-path` 服务器的 path
- `-key` 服务器配置的 websocket key

> 如果你无法链接请检查是否在服务端和客户端都设置了 `Key`。服务端不允许空 `key` 启动（stdio 服务器正常工作）。
>
> 如果内置的 stdio 导致服务器自动退出或其它问题，则可在 `config.json` 中设置 `DisableStdioServer` 为 `true` 以禁用。

---

## 会话管理

支持会话断连延迟释放——客户端断开后会话不会立即销毁，保留上下文状态，允许客户端重新连接后恢复。同时支持后台运行模式，适合长时间执行的命令或任务。

---

## 请求重试

网络请求采用指数退避重试机制，最多重试 3 次。当 LLM Provider 返回临时性错误时自动重试，提升系统稳定性。

---

## 终端与命令执行

命令执行 PTY 实现：

- **Linux**：`/dev/ptmx` PTY
- **macOS**：基于 `openpty`
- **Windows**：管道透传 (ConPTY 无法正确在 Service 中工作)

### 沙箱隔离

| 平台 | 隔离机制 |
|------|----------|
| Linux | mount namespace + 挂载隔离 |
| Windows | 作业对象（Job Object）+ 令牌限制（Token Restrictions） |
| macOS | 暂无 |

---

## 多 Agent 系统

支持动态创建、管理子 Agent（Subagent）：

- 每个子 Agent 可绑定特定工作路径、模型、提示词
- 支持独立的作用域（Scope）控制
- 子 Agent 可自动审批规则独立配置，未配置时回退到全局默认

## Agent 命令

- `/effort (low|medium|high|xhigh|max|[unset])`: 控制推理程度
- `/compress`:  压缩上下文
- `/background (on|[off])`: 后台运行
- `/reload`: 重载配置
- `/approve` 批准工具调用

## ACP 协议扩展

见 `docs` 目录

---

## 自动审批规则（AutoApprove / AutoReject）

自动审批规则使用 `github.com/expr-lang/expr` 实现。这是一个轻量级的表达式语言，支持 C 风格的基本的逻辑运算和函数调用。

### 1. 可配置字段
- 全局：`Agent.AutoApprove` / `Agent.AutoReject`
- Subagent：`Agent.Agents.{subagent}.AutoApprove` / `Agent.Agents.{subagent}.AutoReject`
- 规则空字符串表示不启用；Subagent 为空时会回退到全局默认。

### 2. 规则行为
- **Reject 优先**：命中 AutoReject 直接拒绝，即使同时命中 AutoApprove。
- **Approve 需全需**：所有 ToolCall 都命中 AutoApprove 才批准。
- 程序内置了一套规则，AutoReject 和 AutoApprove 均取或的关系。`AgentsConfig.IgnoreDefaultRules` 设置为 true 后，全局默认规则不生效。**除非你明确知道自己在做什么，否则不建议设置该字段。**

### 3. 可用变量
- `ToolCalls`：完整的 `[]ToolCall` Array
- `ToolCall`：当前工具调用（单个）
- `Agent`：当前 Agent 配置

`ToolCall` 结构：
- `ToolCall.Name`
- `ToolCall.ID`
- `ToolCall.Parameters`（`map[string]*any`，即 json 中 `Object`）

### 4. 可用函数
- `regex(pattern, text)` 正则匹配
- `contains(s, sub)` 字符串包含
- `hasParam(call, key)` 参数存在
- `param(call, key)` 参数值

### 5. 示例
#### 示例 A：仅允许 Read 自动批准（已经内置）
```
AutoApprove: "ToolCall.Name == 'trace'"
AutoReject:  ""
```

#### 示例 B：拒绝任何包含 rm 的命令
```
AutoReject: "contains(ToolCall.Name, 'run') && regex('rm\\s', param(ToolCall, 'command'))"
```

#### 示例 C：只允许 shell 且 command 包含 git status 或 git diff
```
AutoApprove: "ToolCall.Name == 'run' && (contains(param(ToolCall,'command'), 'git status') || contains(param(ToolCall,'command'), 'git diff'))"
```

#### 示例 D：拒绝参数 key=path 且 path 以 /etc 开头
```
AutoReject: "hasParam(ToolCall, 'path') && regex('^/etc', param(ToolCall, 'path'))"
```
