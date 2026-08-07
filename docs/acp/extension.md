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
  }
}
```

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

触发时机：

- 首次正常请求（非 `/` 斜杠命令）完整响应后，服务端异步生成 AI 标题并写入 `Chats.AITitle`。
- `/title` 命令设置或还原用户标题（`Chats.Title`）时。
- 自动/手动 compress 完成后重生成 AI 标题（用户已设置手动标题时跳过）。

客户端可据此刷新会话列表展示。

### 2.2. `alk.cxykevin.top/summary`

- `type` ***string***: 内容类型。固定为 `text`。
- `text` ***string***: 摘要文本。为空意味着摘要启动生成还未结束。

> 摘要若出现异常则直接停止 loop 并在 loop 级别报错。

### 2.3. `alk.cxykevin.top/agent_status`

- `alk.cxykevin.top/agent_status` ***string?*** 当前所在的 SubAgent。其为 `""` 则为处于主 Agent。

当 `sessionUpdate` 为 `agent_thought_chunk`/`agent_message_chunk`/`agent_thought`/`agent_message` 时，`alk.cxykevin.top/agent_status` 存在。

### 2.4. `alk.cxykevin.top/error_msg`

- 挂在 `state_update` 等 update 对象顶层的错误信息扩展（v2 无轮次内错误通道）。`state_update idle` 时若存在非空 `alk.cxykevin.top/error_msg` 表示本轮出错（`stopReason` 为 `refusal`）。

## 3. 方法扩展

### 3.1. `session/resume` 与 `replayFrom`

`session/load` 已在 v2 移除，alkaid0 使用 `session/resume`。`replayFrom` 参数：

- 省略或 `null`：仅重连，不重放历史。
- `{ "type": "start" }`：重放整个对话历史（以 `user_message` / `agent_message` / `agent_thought` 整消息 upsert 形式，携带与直播一致的 `messageId`，客户端据此 upsert 而非重复）。

### 3.2. `session/request_permission`（服务端 → 客户端）

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

### 3.3. `alk.cxykevin.top/config/reload`

重载配置文件。无参数无返回值。异步执行。

### 3.4. `alk.cxykevin.top/config/get` `alk.cxykevin.top/config/set`

获取和设置当前会话的完整配置。这两个方法用于远程读取或修改运行时的配置状态。

#### 3.4.1. `alk.cxykevin.top/config/get`

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

#### 3.4.2. `alk.cxykevin.top/config/set`

写入（部分更新）配置并自动持久化。支持**部分更新**——只有请求中显式指定的字段会被覆盖，未指定的字段保持现有值不变。写入成功后自动保存到配置文件，并触发所有已注册的重载钩子（包括配置广播推送到所有已连接的客户端）。

- **请求参数**：

  ```json
  {
    "config": { ... }
  }
  ```

  - `config` ***object***: 需要更新的配置片段。接受完整的或部分的配置 JSON。支持**深层嵌套字段的部分更新**（如 `Model.defaultModelID`）。

- **响应**：成功时为 `null`。失败时返回错误信息。

- **错误**：

  | 场景 | 错误信息 |
  |---|---|
  | `config` 字段为 `null` / 缺失 | `"config is required"` |
  | `config` 不是合法 JSON | `"invalid JSON config"` |
  | JSON 字段与配置结构不匹配 | `"failed to apply config: ..."` |

### 3.5. `alk.cxykevin.top/session/get_background` / `alk.cxykevin.top/session/get_effort`

- `sessionId` ***string***: 会话 ID。

查询会话后台运行模式（`background` 布尔）与当前推理强度（`effort`，`unset`/`low`/`medium`/`high`/`max`/`xhigh`）。推理强度也可经 `session/set_config_option`（`configId: "thought_level"`）修改。

### 3.6. `alk.cxykevin.top/list_subagent`

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

### 3.7. `session/update`（客户端 → 服务端，双向扩展）

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
