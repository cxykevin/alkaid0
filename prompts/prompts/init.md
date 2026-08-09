{{/* Init 指令：分析代码库并生成 AGENTS.md */}}
<!-- Alkaid Init Prompt -->
# /init Command

Please analyze the codebase in the **current working directory** and create an `AGENTS.md` file, which will be provided to future Alkaid0 agent instances running in this repository.

Use your tools (such as `tree`, `search`, `edit`, `run`) to actively explore the codebase structure, build process, and test process, then generate `AGENTS.md`.

## What AGENTS.md Should Contain

1. **Common Commands**: How to build, lint, and run tests. Include the essential commands needed for development in this codebase, such as how to run a single test.
2. **High-level Architecture and Structure**: So future instances can get up to speed faster. Focus on the "big picture" architecture that requires reading multiple files to understand.

## Writing Requirements

- If an `AGENTS.md` already exists, read it first and **improve it**; otherwise, create a new one.
- Don't repeat yourself, and don't include obvious instructions everyone already knows (such as "provide useful error messages to users", "write unit tests for all new tools", "never put sensitive information in code or commits").
- Avoid listing every easily discoverable component or file structure.
- Don't include generic development practices.
- If there is a `README.md`, read it and include the important parts.
- If there are Cursor rules (`.cursor/rules/` or `.cursorrules`) or Copilot rules (`.github/copilot-instructions.md`), be sure to include the important parts.
- Don't fabricate sections such as "Common Development Tasks", "Development Tips", "Support and Documentation" unless they are explicitly visible in the source code.
- Write in the same language as the repository's existing documentation (this repository's docs are usually in Chinese).

## File Prefix

`AGENTS.md` must start **verbatim** with the following text (as a header preamble):

```
# AGENTS.md

This file provides guidance to Alkaid0 agent when working with code in this repository.
```

When finished, briefly summarize your understanding of the codebase and the status of the generated `AGENTS.md`.
