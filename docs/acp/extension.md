# Alkaid0 对 ACP (Agent Client Protocol) v2 协议的扩展

## 0. 规范

> 由于某些原因，alkaid0 未能按照 [ACP 规范](https://agentclientprotocol.com/protocol/extensibility#the-meta-field) 添加 `_meta` 字段进行扩展。所以所有 alkaid0 扩展的内容均在下方进行描述。

**本文档中所有数据类型描述均采用 `TypeScript` 风格。对于 `number` 等类型，其括号后表示其实际数据类型与范围，如 `number(uint64)` 表示 `number` 的实际范围遵从 uint64。**

---

alkaid0 的 **所有扩展字段均以 `alk.cxykevin.top/` 开头**，并使用 **下划线命名法**，如 `alk.cxykevin.top/summary` 等。

## 0. 协议扩展

alkaid0 所默认实现的协议是简单 `Websocket` 而非 [ACP 中建议的 `stdio`](https://agentclientprotocol.com/protocol/transports) (但 alkaid0 server 中保留了对 stdio 的支持)。

*关于 WebSocket 协议的使用，请参考 README.md 中相关说明。*

服务端会按配置文件描述开启一个 websocket 服务。(此时服务端同时会启动一个标准的 stdio 服务器，但并不建议使用，应优先使用 helper 代理)。

服务端使用 Query 参数认证。在 Query 参数中添加 `key=<key>` 即可。如果没有 Query 参数选项，则可以在 `Path` 中设置 `/acp?k=<key>`。

支持 Websocket 桥接的客户端可以直接链接。只支持 stdio 的客户端可以使用提供的 helper 链接。

Websocket 的每个请求体与 stdio 下的每个请求体均相同。

## 1. 特殊行为

### 1.1. 初始化

alkaid0 支持 [ACP v2 初始化](https://agentclientprotocol.com/protocol/v2/initialization)：`initialize` 返回 `protocolVersion: 2`、`capabilities`、`info` 与空 `authMethods`（空数组 = 客户端不得调用 `auth/login`/`auth/logout`，alkaid0 也不注册这两个方法）。

alkaid0 对于客户端并 **不强制** 要求客户端初始化，这与 [ACP 规范](https://agentclientprotocol.com/protocol/initialization) 不同。

服务端能力声明（ACP v2，标记均为 `{}` 对象而非布尔）：

```json
{
  "session": {
    "prompt": { "image": {}, "embeddedContext": {} },
    "delete": {}
  },
  "alk.cxykevin.top/alkaid0/v0.4": {},
  "alk.cxykevin.top/alkaid0/v0.5": {}
}
```

其中 `alk.cxykevin.top/alkaid0/v0.4` 与 `alk.cxykevin.top/alkaid0/v0.5` 为 alkaid0 扩展协议版本能力标记。v0.5 包含 dynworkflow stdout 握手、workflow stdio 控制、workflow 状态持久化以及 workflow/update_xxx 实时广播协议；v0.4 能力保持兼容。

`session/list`、`session/resume`、`session/close` 是 `session` 基线能力，无需标记。

### 1.2. 多客户端支持

alkaid0 支持 **同一会话被多个客户端链接**。对于多客户端的情形，客户端会收到来自其它客户端的操作广播(所有消息的方式与 ACP 协议相同)。

客户端在其它客户端发起提示后，会收到 `session/update` 且 `sessionUpdate` 字段为 `user_message`（携带 `messageId`）的更新，随后收到 `state_update` 且 `state` 为 `running` 的更新。发起提示词的客户端自身 **也会接收到广播**。

在 [ACP v2 prompt 生命周期](https://agentclientprotocol.com/protocol/v2/prompt-turn) 中，`session/prompt` 的响应为立即的 `{}` 确认（纯 ack）。所有客户端（包括发起者）通过 `state_update` 的 `state: "running"` / `state: "idle"`（携带 `stopReason`）感知一轮前台工作的开始与结束。

## 2. `session/update` 扩展

> 下列标题均指 `session/update` 的 `sessionUpdate` 字段。

### 2.1. 标准事件（ACP v2）

alkaid0 现在遵循 ACP v2 标准事件，且字段置于 update 对象**顶层**（非 `content` 包装）：

- **消息**：`user_message` / `agent_message` / `agent_thought`（整消息 upsert，携带 `messageId` 与完整 `content` 数组）；`user_message_chunk` / `agent_message_chunk` / `agent_thought_chunk`（流式 chunk，携带 `messageId` 与单个 content 块）。
- **`state_update`**：`state` 为 `running` / `idle` / `requires_action`；`idle` 时携带 `stopReason`（`end_turn` / `max_tokens` / `max_turn_requests` / `refusal` / `cancelled`）。`session/prompt` 返回 `{}` 后按此驱动轮次状态。
- **`tool_call_update`**：首次出现某 `toolCallId` 创建调用，后续按 omit/`null`/value patch。`status` 取值 `pending` / `streaming` / `completed` / `cancelled`（`streaming` 为 alkaid0 的流式增量预览，0.1s 限流推送完整快照）。
- **`plan_update`**：`plan` 字段形如 `{ "type": "items", "planId": "plan_<chatID>", "entries": [...] }`。
- **`usage_update`**：`used` / `size`（`used` = 累计 token，`size` = 当前模型 `TokenLimit`）。
- **`config_option_update`**：`configOptions` 字段（顶层）。
- **`available_commands_update`**：`availableCommands` 字段，命令 `input` 形如 `{ "type": "text", "hint": "..." }`。
- **`session_info_update`**：会话元数据更新（标题/最后活动时间），字段置顶层。`title` 为会话最终展示标题（用户设置的标题优先，其次 AI 生成的标题）；`updatedAt` 为 RFC 3339 最后活动时间。
- **`tool_call_update` 的 `alk.cxykevin.top/run_id`**：后台 `run` 成功提交后，随对应工具调用回调在 update 顶层返回 run ID；提交失败时不返回。该 ID 形如 `@temp/run/1`，只保证在当前 workspace 内唯一，使用 base36 序列以便 AI 在后续 `wait` 调用中引用。

### 2.2. `alk.cxykevin.top/terminal_update`

终端生命周期与内容更新通知。所有字段位于 `update` 顶层，`terminals` 始终表示当前终端的全量快照。后台 shell 的 `terminalId` 使用工具返回的 `@temp/run/<id>`，因此客户端可直接用同一个 ID 查询或停止终端。

- `updateType` ***string***：`full` 或 `incremental`，区分全量和增量推送。
- `terminals` ***object[]***：当前会话活动终端的完整列表；全量推送时包含每个终端的完整 `content`。任务结束后发送的 `stop` 更新中，该任务已从活动终端列表移除。
- `terminalId` ***string?***：增量推送涉及的终端 ID。
- `status` ***string?***：终端状态；`start` 表示创建，`running` 表示运行中，`stop` 表示终端会话结束。
- `content` ***string?***：指定终端当前内容；状态查询和增量更新均会携带。

全量推送示例：

```json
{
  "sessionId": "sess_1:/workspace",
  "update": {
    "sessionUpdate": "alk.cxykevin.top/terminal_update",
    "updateType": "full",
    "terminals": [
      {
        "terminalId": "run_1",
        "sessionId": "sess_1:/workspace",
        "kind": "background",
        "status": "running",
        "command": "sleep 60",
        "content": "...",
        "createdAt": "2025-01-01T00:00:00Z"
      }
    ]
  }
}
```

增量推送示例：

```json
{
  "update": {
    "sessionUpdate": "alk.cxykevin.top/terminal_update",
    "updateType": "incremental",
    "terminalId": "run_1",
    "status": "running",
    "content": "最新终端内容",
    "terminals": [ ... ]
  }
}
```

触发时机：

- `session/resume` 完成连接注册后立即推送一次 `full` 快照。
- 终端启动、后台状态刷新、终端结束时推送 `incremental` 更新；`stop` 表示客户端应结束该终端会话。
- `alk.cxykevin.top/session/terminal/status` 成功调用时，除 RPC 响应外，还会通过请求 callback 立即推送一条 `full` 更新，包含该终端的完整内容。

### 2.3. `alk.cxykevin.top/shell_stop`

后台 `run` 工具创建的 shell 任务结束时，服务端广播该事件。事件字段位于 `update` 顶层，不写入 `_meta` 或 `body`：

- `runId` ***string***：后台任务 ID。
- `terminalId` ***string***：对应终端 ID，与后台 `run` 返回的 `@temp/run/<id>` 相同。
- `command` ***string***：脱敏后的展示命令。
- `status` ***string***：固定为 `stop`。
- `success` ***boolean***：命令是否成功结束。
- `killed` ***boolean***：命令是否因停止/取消而结束。

当会话处于 `idle` 或等待状态时，服务端会把 shell 停止信息作为内部运行时事件重新注入 loop，并触发新一轮模型请求；该通知不是用户消息，不会写入对话历史。若原 loop 已退出，服务端会创建新的 loop 后再注入通知，不复用已关闭的生命周期通道。loop 正在请求模型、执行工具或等待审批时不会并发打断当前轮次。

示例：

```json
{
  "sessionId": "sess_1:/workspace",
  "update": {
    "sessionUpdate": "alk.cxykevin.top/shell_stop",
    "runId": "run_1",
    "terminalId": "run_1",
    "command": "npm test",
    "status": "stop",
    "success": true,
    "killed": false
  }
}
```

### 2.4. `alk.cxykevin.top/agent_status`

触发时机：

- 首次正常请求（非 `/` 斜杠命令）完整响应后，服务端异步生成 AI 标题并写入 `Chats.AITitle`。
- `/title` 命令设置或还原用户标题（`Chats.Title`）时。
- 自动/手动 compress 完成后重生成 AI 标题（用户已设置手动标题时跳过）。

客户端可据此刷新会话列表展示。

### 2.5. `alk.cxykevin.top/summary`

- `type` ***string***: 内容类型。固定为 `text`。
- `text` ***string***: 摘要文本。为空意味着摘要启动生成还未结束。

> 摘要若出现异常则直接停止 loop 并在 loop 级别报错。

### 2.6. `alk.cxykevin.top/error_msg`

- 挂在 `state_update` 等 update 对象顶层的错误信息扩展（v2 无轮次内错误通道）。`state_update idle` 时若存在非空 `alk.cxykevin.top/error_msg` 表示本轮出错（`stopReason` 为 `refusal`）。

## 3. 方法扩展

### 3.1. `alk.cxykevin.top/session/terminal/list` / `status` / `stop`

这三个私有方法均通过 `sessionId` 校验会话，并对 terminal 执行会话归属校验。

#### `alk.cxykevin.top/session/terminal/list`

请求：`{ "sessionId": string }`。返回 `{ "terminals": TerminalInfo[] }`，只包含当前活动终端；每个终端包含 `terminalId`、`sessionId`、`kind`、`status`、`command`、`reason`、`agentId`、`toolId`、`content`、`createdAt`。

#### `alk.cxykevin.top/session/terminal/status`

请求：`{ "sessionId": string, "terminalId": string }`。返回 `{ "terminal": TerminalInfo }`，包含当前终端完整内容。成功时还会通过该请求的 callback 立即发送一条 `alk.cxykevin.top/terminal_update`，其 `updateType` 为 `full`。

#### `alk.cxykevin.top/session/terminal/stop`

请求：`{ "sessionId": string, "terminalId": string }`。返回 `{ "terminalId": string, "status": "kill_requested" }`。终止是异步的，完成清理后通过 `alk.cxykevin.top/terminal_update` 发送 `status: "stop"`。

### 3.2. dynworkflow stdout 与 workflow 私有方法

只有 `run` 工具的 `type: "python"` 可以触发 workflow。Python 源码包含 `import dynworkflow`、`import dynworkflow as ...` 或 `from dynworkflow import ...` 时，服务端强制将任务后台化，并注入 `ALKAID0_WORKFLOW_REPORT=1` 和会话标识。不会注册 workflow/start 方法。

Python run 的 `runId` 是 workflow 的唯一关联标识，同时绑定 Job、terminal、tool call、数据库记录和控制通道。只有已由该 Python run 的合法 stdout 事件登记的 runId 才能使用 workflow 控制协议。

#### stdout 握手与过滤

`Flow.run()` 的 stdout 使用 bracketed-paste 握手：开始标记为 `\u001b[?2004h`，结束标记为 `\u001b[?2004l`。握手帧内每行是一个 JSONL 事件。服务端按增量数据解析，因此标记可以跨 read 分片。握手标记、帧内 JSON、非法协议行以及 dynworkflow 交互内容从 terminal 输出中剔除；帧外普通 stdout 和 stderr 仍作为终端输出。

#### 事件更新

workflow 事件按 runId 持久化 graph、当前 node/agent 状态和日志，并广播为以下顶层 `session/update`：`alk.cxykevin.top/session/terminal/workflow/update_graph`、`update_node`、`update_agents_start`、`update_agent`、`update_node_code`、`update_node_log`。公共字段 `sessionId`、`runId`、`sessionUpdate`、`eventType`、`time`、`workflow` 以及事件字段直接放在 `update` 顶层，不放 `_meta` 或 `body`。graph 是完整快照，其余事件是增量更新。

#### workflow 控制与查询方法

不会注册 `alk.cxykevin.top/session/terminal/workflow/start`。workflow 只能由 Python run 自动产生。提供以下方法：

- `alk.cxykevin.top/session/terminal/workflow/status`：请求 `{ "sessionId": string, "runId": string }`，返回 workflow、terminal、graph、当前 agent 状态和日志。
- `alk.cxykevin.top/session/terminal/workflow/input`：按 runId 将受校验的控制对象写入 Python stdin。允许 shutdown、node terminate/restart、agent terminate/retry。
- `alk.cxykevin.top/session/terminal/workflow/stop`：写入 shutdown，必要时复用 terminal kill 强制终止；操作幂等。
- `alk.cxykevin.top/session/terminal/workflow/list`：请求 `{ "sessionId": string }`，返回当前会话 workflow 列表。

所有方法执行 session、runId 和 terminal 所有权校验。shell、sleep、wait 和普通 Python run 不能作为 workflow 控制目标。input/stop 仅允许活动 workflow，status/list 可读取已结束记录。

#### 数据库

服务端通过 AutoMigrate 保存 `Workflows` 和 `WorkflowEvents`：前者保存 runId、workflowId、ChatID、TerminalID、状态、时间、最新 graph、当前 node/agent 状态和最后日志序号；后者按 runId 和 sequence 保存结构化事件及 payload，用于 status 查询和断线恢复。

### 3.3. `session/resume` 与 `replayFrom`

- 省略或 `null`：仅重连，不重放历史。
- `{ "type": "start" }`：重放整个对话历史（以 `user_message` / `agent_message` / `agent_thought` 整消息 upsert 形式，携带与直播一致的 `messageId`，客户端据此 upsert 而非重复）。

### 3.4. `session/request_permission`（服务端 → 客户端）

工具待审批（自动审批规则未命中）时，alkaid0 按 [ACP v2 权限](https://agentclientprotocol.com/protocol/v2/tool-calls#requesting-permission) 发起 `session/request_permission` 请求：

```json
{
  "jsonrpc": "2.0",
  "id": "perm_1",
  "method": "session/request_permission",
  "params": {
    "sessionId": "sess_1:/path",
    "title": "Approve tool call: edit",
    "subject": {
      "type": "tool_call",
      "toolCall": {
        "toolCallId": "call_1_2_tid",
        "title": "[Call edit]tid",
        "kind": "edit",
        "status": "pending",
        "content": [
          { "type": "alk.cxykevin.top/calling_info", "name": "edit", "args": { ... } }
        ]
      }
    },
    "options": [
      { "optionId": "allow_once", "name": "Allow once", "kind": "allow_once" },
      { "optionId": "reject_once", "name": "Reject once", "kind": "reject_once" }
    ]
  }
}
```

客户端回包（响应本请求的 id）：

```json
{ "jsonrpc": "2.0", "id": "perm_1", "result": { "outcome": "selected", "optionId": "allow_once" } }
```

语义：

- `outcome: "selected"` 且 `optionId: "allow_once"` → 批准，工具执行后继续。
- `optionId: "reject_once"` 或 `outcome: "cancelled"` → 拒绝（等价 cancel）：待审工具广播 `tool_call_update(status=cancelled)`，随后 `state_update idle(stopReason=cancelled)`，本轮结束，不执行工具。

### 3.5. `alk.cxykevin.top/config/reload`

重载配置文件。无参数，异步执行。成功时对带 ID 的请求返回 `result: null` 响应（不挂起客户端）。

### 3.6. `alk.cxykevin.top/config/get` `alk.cxykevin.top/config/set`

获取和设置当前会话的完整配置。这两个方法用于远程读取或修改运行时的配置状态。

#### 3.6.1. `alk.cxykevin.top/config/get`

获取完整的当前配置。

- **请求参数**：无（`ConfigGetRequest` 为空对象）

  ```json
  {}
  ```

- **响应**：

  ```json
  {
    "config": { ... }
  }
  ```

  - `config` ***object***: 完整的全局配置对象。结构见 [`config/structs/structs.go`](https://github.com/cxykevin/alkaid0/blob/main/config/structs/structs.go)。

> **注意**：返回值为全局配置的直接指针引用，响应内容随后台配置变化实时更新。

#### 3.6.2. `alk.cxykevin.top/config/set`

写入（部分更新）配置并自动持久化。支持**部分更新**——只有请求中显式指定的字段会被覆盖，未指定的字段保持现有值不变。写入成功后自动保存到配置文件，并触发所有已注册的重载钩子（包括配置广播推送到所有已连接的客户端）。

- **请求参数**：

  ```json
  {
    "config": { ... }
  }
  ```

  - `config` ***object***: 需要更新的配置片段。接受完整的或部分的配置 JSON。支持**深层嵌套字段的部分更新**（如 `Model.defaultModelID`）。

- **响应**：成功时返回空对象 `{}`（`result` 非空，确保带 ID 的请求能收到响应而非永久等待）。失败时返回错误信息。

- **错误**：

  | 场景 | 错误信息 |
  |---|---|
  | `config` 字段为 `null` / 缺失 | `"config is required"` |
  | `config` 不是合法 JSON | `"invalid JSON config"` |
  | JSON 字段与配置结构不匹配 | `"failed to apply config: ..."` |

### 3.7. `alk.cxykevin.top/session/get_background` / `alk.cxykevin.top/session/get_effort`

- `sessionId` ***string***: 会话 ID。

查询会话后台运行模式（`background` 布尔）与当前推理强度（`effort`，`unset`/`low`/`medium`/`high`/`max`/`xhigh`）。推理强度也可经 `session/set_config_option`（`configId: "thought_level"`）修改。

### 3.8. `alk.cxykevin.top/list_subagent`

- `sessionId` ***string***: 会话 ID。

列出当前项目所有的 Agents 和 Tags。

返回值：

- `agents` ***object[]***: 所有 Agents。
  - `name` ***string***: Agent 名称。
  - `tag` ***string***: 使用的 Agent Tag。
  - `path` ***string***: Agent 绑定到的路径。
- `tags` ***object[]***: 所有 Tags。
  - `name` ***string***: Agent Tag 名称。
  - `id` ***string***: Tag ID。
  - `modelID` ***number(int32)***: Tag 对应的 Model ID。
  - `color` ***string***: 展示颜色。Hex 格式。
  - `autoApproveExpr` ***string***: 自动批准表达式。`github.com/expr-lang/expr` 格式。
  - `autoRejectExpr` ***string***: 自动拒绝表达式。`github.com/expr-lang/expr` 格式。
  - `description` ***string***: Tag 描述。人类可读。
  - `prompt` ***string***: Agent Tag LLM 完整提示词。
  - `shortPrompt` ***string***: Agent Tag LLM 简短提示词。在 Agent 激活前使用。

### 3.9. `session/update`（客户端 → 服务端，双向扩展）

ACP v2 中 `session/update` 是服务端 → 客户端的通知（含 `session_info_update` 变体）。alkaid0 同时将其注册为客户端可调用的**请求方法**，用于重命名会话标题。请求体与标准通知同构：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/update",
  "params": {
    "sessionId": "sess_293:/path/to/project",
    "update": {
      "sessionUpdate": "session_info_update",
      "title": "Implement user authentication"
    }
  }
}
```

语义：

- 当前仅支持 `session_info_update` 变体（标题更新）。其他变体返回错误。
- `title`：非空 = 设置用户标题（展示优先）；空串 = 清除用户标题（回退 AI 标题）；`null` 或省略 = 不修改。
- **会话无需预先 `session/new` / `session/resume`**：`sessionId` 未在内存注册表时，服务端按字符串解析 `cwd`+`id` 并经数据库校验会话真实存在后落库。
- 变更单列写入 `Chats.title`（`updated_at` 随之刷新），并向该会话所有已连接客户端广播标准 `session_info_update` 通知（含发起者）。
- 成功返回 `{}`。

## 4. 字段扩展

### 4.1. [Tool Calls 的 Content 字段](https://agentclientprotocol.com/protocol/v2/tool-calls#content)

`tool_call_update` 的 `content` 为 `ToolCallContent[]` 数组。alkaid0 每个元素为：

- `type="content"` 的标准内容块（`content` 内为 `{ "type": "text", "text": ... }`）。
- `type="alk.cxykevin.top/calling_info"` ***object*** 对工具原始调用参数的对象格式的表示。该字段对于 alkaid0 工具调用 **必然存在**。

  - `name` ***string***: 工具原始名称。
  - `messageID` ***number(uint64)***: 工具原始调用消息 ID。
  - `args` ***object***: 工具调用参数。

> 注：ACP v2 约定实现自定义 type 以 `_` 开头，`alk.cxykevin.top/calling_info` 不含 `_`。因该字段为 alkaid0 自有客户端消费，维持现状（已知合规性问题）。

### 4.2. 配置选项（`configId` 与 `thought_level`）

`session/new` / `session/resume` 响应与 `config_option_update` 中的 `configOptions` 遵循 ACP v2：`configId`（非 `id`）、`name`、`description`、`category`、`type`、`currentValue`、`options`。当前提供：

- `model`（category `model`）：模型选择。
- `thought_level`（category `thought_level`）：推理强度，可选值 `unset`/`low`/`medium`/`high`/`max`/`xhigh`，经 `session/set_config_option`（`configId: "thought_level"`，`type: "id"`）修改。

## 5. ID 生成逻辑

> 本部分说明了 alkaid0 中对应 ACP 各部分 ID 的生成逻辑。

### 5.1. `sessionId`

sessionID 遵从以下格式：

```text
sess_<realSessionID>:<sessionPath>
```

- `realSessionID` ***number(uint64)***: 实际数据库中的 session ID。
- `sessionPath` ***string***: session 对应工作区的路径。

> 服务端只使用 `realSessionID` 进行操作，但会校验 `sessionPath`。

### 5.2 `modelId`

modelId 遵从以下格式：

```text
<realModelID>/<modelConfigID>
```

- `realModelID` ***number(int32)***: 配置文件中指定的 model ID (key)。
- `modelConfigID` ***number(int32)***: 配置文件中指定的用于实际请求 model ID (value 中 `modelID` 字段)。

> 服务端只使用 `realModelID` 进行操作。`modelConfigID` 会被忽略但其必须不为空。如 `1/a` 是合法的（哪怕其实际的模型 ID 为 `echo-flash`）。

### 5.3 `messageId`

- DB 消息（用户/Agent/Thought）：`msg_<dbID>`，`dbID` 为 `Messages` 表自增 ID。直播与 `session/resume` 回放使用同一推导，客户端据此 upsert。
- 斜杠命令用户消息（不入库）：`cmd_<chatID>_<seq>`，`seq` 为服务端递增序号。
