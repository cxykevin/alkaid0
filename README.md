# Alkaid0

Alkaid0 是一个模块化的 AI Coding 工具 与 Agent 框架，专为构建具备多 Agent 能力、工具调用系统和流式响应处理功能的智能编码助手而设计。该框架基于 Go 语言构建。

设计理念：**低消耗** **用户友好** **可扩展** **强兼容性**

### 日志（test only）

路径 `~/.config/alkaid0/log.log`

> 日志经过脱敏处理（脱去 Provider URL / KEY），但会保留请求的 Model ID 和 Agent Name 以及完整输入输出。日志不会携带工作区信息但 AI 模型的输出可能会包含部分用户代码。

提供 toolkit 包以查看日志。用法见其 `--help`。

### 配置（test only）

路径 `~/.config/alkaid0/config.json`

```json
{
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
                "EnableThinking": true
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
    "ThemeID": 0
}
```

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
#### 示例 A：仅允许 Read 自动批准
```
AutoApprove: "ToolCall.Name == 'Read'"
AutoReject:  ""
```

#### 示例 B：拒绝任何包含 rm 的命令
```
AutoReject: "contains(ToolCall.Name, 'shell') && regex('rm\\s', param(ToolCall, 'command'))"
```

#### 示例 C：只允许 shell 且 command 包含 git status 或 git diff
```
AutoApprove: "ToolCall.Name == 'shell' && (contains(param(ToolCall,'command'), 'git status') || contains(param(ToolCall,'command'), 'git diff'))"
```

#### 示例 D：拒绝参数 key=path 且 path 以 /etc 开头
```
AutoReject: "hasParam(ToolCall, 'path') && regex('^/etc', param(ToolCall, 'path'))"
```
