# Alkaid0 对 ACP (Agent Client Protocol) 协议的扩展

## 0. 规范

> 由于某些原因，alkaid0 未能按照 [ACP 规范](https://agentclientprotocol.com/protocol/extensibility#the-meta-field) 添加 `_meta` 字段进行扩展。所以所有 alkaid0 扩展的内容均在下方进行描述。

**本文档中所有数据类型描述均采用 `TypeScript` 风格。对于 `number` 等类型，其括号后表示其实际数据类型与范围，如 `number(uint64)` 表示 `number` 的实际范围遵从 uint64。**

---

alkaid0 的 **所有扩展字段均以 `alk.cxykevin.top/` 开头**，并使用 **下划线命名法**，如 `alk.cxykevin.top/session_start` 等。

## 0. 协议扩展

alkaid0 所默认实现的协议是简单 `Websocket` 而非 [ACP 中建议的 `stdio`](https://agentclientprotocol.com/protocol/transports) (但 alkaid0 server 中保留了对 stdio 的支持)。

*关于 WebSocket 协议的使用，请参考 README.md 中相关说明。*

服务端会按配置文件描述开启一个 websocket 服务。(此时服务端同时会启动一个标准的 stdio 服务器，但并不建议使用，应优先使用 helper 代理)。

服务端使用 Query 参数认证。在 Query 参数中添加 `key=<key>` 即可。如果没有 Query 参数选项，则可以在 `Path` 中设置 `/acp?k=<key>`。

支持 Websocket 桥接的客户端可以直接链接。只支持 stdio 的客户端可以使用提供的 helper 链接。

Websocket 的每个请求体与 stdio 下的每个请求体均相同。

## 1. 特殊行为

### 1.1. 初始化

alkaid0 对于客户端并 **不强制** 要求客户端初始化，这与 [ACP 规范](https://agentclientprotocol.com/protocol/initialization) 不同。

### 1.2. 多客户端支持

alkaid0 支持 **同一会话被多个客户端链接**。对于多客户端的情形，客户端会收到来自其它客户端的操作广播(所有消息的方式与 ACP 协议相同)。

客户端在其它客户端发起提示后，会收到 `session/update` 且 `sessionUpdate` 字段为 `alk.cxykevin.top/session_start` 的更新。发起提示词的客户端自身 **也会接收到广播**。

在多客户端链接的情况下，只有 **发起提示 (prompt 请求) 的客户端** 会收到 [ACP 原生的 `end_turn` 响应](https://agentclientprotocol.com/protocol/prompt-turn)。其它客户端会收到 `session/update` 且 `sessionUpdate` 字段为 `alk.cxykevin.top/session_stop` 的更新。发起提示词的客户端自身 **也会接收到广播**。

## 2. `session/update` 扩展

> 下列标题均指 `session/update` 的 `sessionUpdate` 字段，内容则均为 `context` 字段中值。

### 2.1. `alk.cxykevin.top/session_start`

`context` 字段始终为 `{}` (空对象)。

### 2.2. `alk.cxykevin.top/session_stop`

- `stopReason` ***string***: 停止原因。同 [ACP 文档中 `session/prompt`](https://agentclientprotocol.com/protocol/prompt-turn#stop-reasons)。

- `alk.cxykevin.top/error_msg` ***string?***: 错误信息。当该字段存在并且值不为 `null` 或 `""` 时则意味着出现错误。*ACP 暂时并未提供一个合理的错误处理方案。因此该字段暂时实现了简单的错误处理。*

## 3. 方法扩展

### 3.1. `session/set_model` `session/setModel` `unstable_setSessionModel`

出于对部分客户端的兼容需要。已经被 [ACP 的会话配置](https://agentclientprotocol.com/protocol/session-config-options) 替代。其语法同 [`session/set_mode`](https://agentclientprotocol.com/protocol/session-modes#setting-the-current-mode)。

- `sessionId` ***string***: 会话 ID。
- `modelID` ***string***: 模型 ID。

## 4. 字段扩展

### 4.1. [`session/prompt` 的响应](https://agentclientprotocol.com/protocol/prompt-turn#4-check-for-completion)

- `alk.cxykevin.top/error_msg` ***string?***: 错误信息。当该字段存在并且值不为 `null` 或 `""` 时则意味着出现错误。*ACP 暂时并未提供一个合理的错误处理方案。因此该字段暂时实现了简单的错误处理。*

### 4.2. [Tool Calls 的 Content 字段](https://agentclientprotocol.com/protocol/tool-calls#content)

- `type="alk.cxykevin.top/calling_info"` ***object*** 对工具原始调用参数的对象格式的表示。该字段对于 alkaid0 工具调用 **必然存在**。
  
  - `name` ***string***: 工具原始名称。
  - `messageID` ***number(uint64)***: 工具原始调用消息 ID。

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
