{{/* Init 指令：分析代码库并生成 AGENTS.md */}}
<!-- Alkaid Init Prompt -->
# /init 指令

请分析**当前工作目录**的代码库，并创建一个 `AGENTS.md` 文件，该文件将提供给未来在此仓库中运行的 Alkaid0 agent 实例使用。

请使用你的工具（如 `tree`、`search`、`edit`、`run`）主动探索代码库结构、构建方式与测试方式，然后生成 `AGENTS.md`。

## AGENTS.md 需要包含的内容

1. **常用命令**：如何构建、lint 和运行测试。包含在此代码库中开发所需的必要命令，例如如何运行单个测试。
2. **高层代码架构与结构**：以便未来的实例能更快地投入工作。重点放在需要阅读多个文件才能理解的"大图景"架构上。

## 编写要求

- 如果已有 `AGENTS.md`，请先阅读并**改进它**；否则创建新的。
- 不要重复自己，不要包含明显、人人都懂的指令（如"向用户提供有用的错误消息"、"为所有新工具编写单元测试"、"绝不将敏感信息放入代码或提交中"）。
- 避免列出每个容易发现的组件或文件结构。
- 不要包含泛泛的开发实践。
- 如果有 `README.md`，请阅读并包含重要部分。
- 如果有 Cursor 规则（`.cursor/rules/` 或 `.cursorrules`）或 Copilot 规则（`.github/copilot-instructions.md`），请务必包含重要部分。
- 除非已在源码中明确看到，否则不要编造"常见开发任务"、"开发技巧"、"支持与文档"等章节。
- 请用与仓库现有文档一致的语言撰写（该仓库文档通常为中文）。

## 文件前缀

`AGENTS.md` 必须以以下文本**逐字开头**（作为文件头部引导语）：

```
# AGENTS.md

This file provides guidance to Alkaid0 agent when working with code in this repository.
```

完成后，请简要总结你对代码库的理解以及 AGENTS.md 的生成情况。
