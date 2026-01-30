### Commands

```text
/help
/model 1
/model 2
/model 5
```

### Basic

```text
你是谁
你是哪家公司开发的
Alkaid0 是哪家公司开发的
你可以使用哪些工具
你可以帮我做什么
```

### Scopes

```text
哪些 scopes 可用
启用 test scope
禁用 test scope
```

### Tools

```text
跟踪 index.html
新建一个 src 文件夹
新建一个 src 文件夹，并在其中新建一个 index.txt 文件，写 aaa，再新建一个 dst 文件夹，并在其中新建一个 index.txt 文件，写 bbb
互换 src 文件夹和 dst 文件夹中的 index.txt 文件。不要查看文件内容或者编辑这两个文件。
```

### Tasks

```text
编写一个炫酷的 hello world 程序到 index.html
使用 Vue 编写一个炫酷的 hello world 程序，参考标准的 vue 项目格式，使用 .vue 组件。
```

### Agent

```text
/agent
/agent add Front frontend .
/agent activate Front 你好
使用 activate_agent 启用 `Front` Agent
```

### Tools Origin

### Think

```text
<think>你好aaaaaaaaaaaaaaaaaa</think>你好世界
```

#### Agent

```json
Test <tools>[{"name":"activate_agent","id":"activate_agent_1","parameters":{"name":"Front","prompt":"..."}}]</tools>
```
