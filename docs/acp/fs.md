# Alkaid0 Filesystem over ACP 协议扩展

## 概述

Alkaid0 提供了一组通过 ACP (Agent Client Protocol) 对服务端文件系统进行操作的接口。所有接口以 `alk.cxykevin.top/fs` 为前缀。

> 所有接口**均需要先建立会话（session/new）** 后使用。`sessionId` 为必填参数。

## 通用约束

### 路径约束

`path` 参数必须遵守以下规则，否则返回 JSON-RPC 错误：

- 必须是**相对路径**（不能以 `/` 或盘符开头）
- 不能包含 `.` 或 `..` 分量
- 必须在会话对应的工作目录（`cwd`）之内
- 不能访问 `.alkaid0` 目录

### IO 超时

所有文件系统操作均有 **200ms（0.2s）** 的超时限制。超时后会返回 `filesystem operation timed out` 错误。

### 错误处理

所有错误均通过 JSON-RPC 的标准错误响应返回。错误码为 `-32099`（服务器错误），错误消息为具体的错误描述。

## 方法列表

| 方法 | 描述 |
|------|------|
| `alk.cxykevin.top/fs/stat` | 获取文件/文件夹信息 |
| `alk.cxykevin.top/fs/read` | 读取文件内容或列出目录 |
| `alk.cxykevin.top/fs/write` | 写入文件（不存在则创建） |
| `alk.cxykevin.top/fs/mkdir` | 递归创建目录 |
| `alk.cxykevin.top/fs/rm` | 递归删除文件或目录 |
| `alk.cxykevin.top/fs/chmod` | 更改文件权限 |
| `alk.cxykevin.top/fs/chown` | 更改文件所有者 |

---

## 1. `alk.cxykevin.top/fs/stat`

获取文件或文件夹的详细信息。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "src/main.go"
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。

### 响应

```json
{
    "size": 1234,
    "permissions": "0644",
    "owner": "user",
    "type": "file"
}
```

- `size` ***number?***: 文件大小（字节）。仅文件有此字段，目录无此字段。
- `permissions` ***string***: 权限字符串，八进制格式。
  - Linux: 返回真实的 Unix 权限位，如 `"0644"`、`"0755"`。
  - Windows: 只读文件映射为 `"0555"`，其它文件映射为 `"0755"`。
- `owner` ***string***: 文件所有者的用户名。
  - Linux: 通过系统调用获取实际所有者。
  - Windows: 返回当前用户。
- `type` ***string***: 文件类型。`"file"` 或 `"directory"`。

---

## 2. `alk.cxykevin.top/fs/read`

读取文件内容或列出目录。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "src/main.go",
    "binary": false,
    "offset": 0,
    "length": 1024
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。
- `binary` ***boolean?***: 是否以二进制模式读取。默认 `false`。为 `true` 时 content 返回 Base64 编码。
- `offset` ***number?***: 起始读取位置（字节偏移）。默认从头开始。
- `length` ***number?***: 读取字节数。默认读取全部。

### 响应（文件）

```json
{
    "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
}
```

`content` ***string***: 文件内容。`binary=true` 时返回 Base64 编码字符串。

### 响应（目录）

```json
{
    "content": [
        { "name": "main.go", "type": "file", "size": 1234 },
        { "name": "lib", "type": "directory" },
        { "name": "test", "type": "directory" }
    ]
}
```

`content` ***object[]***: 目录条目列表。每个条目包含：

- `name` ***string***: 文件名。
- `type` ***string***: 类型。`"file"` 或 `"directory"`。
- `size` ***number?***: 文件大小（字节）。仅文件有该字段。

> `.alkaid0` 目录会自动从列表结果中过滤掉。

---

## 3. `alk.cxykevin.top/fs/write`

写入文件内容。文件不存在时会自动创建，父目录不存在时也会自动创建。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "src/output.txt",
    "content": "Hello, World!",
    "binary": false,
    "append": false
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。
- `content` ***string***: 文件内容。`binary=true` 时需传入 Base64 编码字符串。
- `binary` ***boolean?***: 是否二进制模式。默认 `false`。为 `true` 时 `content` 为 Base64 编码。
- `append` ***boolean?***: 是否追加模式。默认 `false`（覆盖写入）。为 `true` 时在文件末尾追加。

### 响应

```json
{
    "bytesWritten": 13
}
```

- `bytesWritten` ***number***: 实际写入的字节数。

---

## 4. `alk.cxykevin.top/fs/mkdir`

递归创建目录（类似 `mkdir -p`）。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "src/components/utils"
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。

### 响应

```json
{}
```

---

## 5. `alk.cxykevin.top/fs/rm`

递归删除文件或目录（类似 `rm -rf`，无确认提示）。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "node_modules"
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。

### 响应

```json
{}
```

---

## 6. `alk.cxykevin.top/fs/chmod`

更改文件或目录的权限。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "script.sh",
    "mode": "0755"
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。
- `mode` ***string***: 权限模式，八进制字符串格式，如 `"0755"`、`"0644"`。

### 响应

```json
{}
```

### 平台差异

- **Linux**: 直接调用 `os.Chmod`，按传入的 mode 完整设置权限位。
- **Windows**: 仅关注 mode 的所有者读位（第 6 位，`0400`）。若该位为 0（禁止所有者读），则将文件设为只读属性；否则设为可读写。其它权限位变化被静默忽略。

---

## 7. `alk.cxykevin.top/fs/chown`

更改文件或目录的所有者。

### 请求

```json
{
    "sessionId": "sess_1:/home/user/project",
    "path": "data.db",
    "owner": "www-data"
}
```

- `sessionId` ***string***: 会话 ID。
- `path` ***string***: 相对路径。
- `owner` ***string***: 用户名。

### 响应

```json
{}
```

### 平台差异

- **Linux**: 通过 `os/user.Lookup(owner)` 查找用户 UID/GID 后调用 `os.Chown`。
- **Windows**: `os.Chown` 在 Windows 上不支持，会返回错误。
