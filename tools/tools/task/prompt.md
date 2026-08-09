###### Virtual Object: `@task`

The `@task` is a markdown task list. Edit it by calling the `edit` tool with `path: "@task"`.

#### Data Syntax

1. Each line: `- [X] taskName: taskDetails` (only `-` bullet, no `*`/`+`/numbers).
2. Status: `[ ]` (waiting), `[-]` (doing), `[X]` (done).
3. Nesting: exactly **2 spaces** per level.
4. `taskName` and `taskDetails` are separated by the **first** `:`.

#### Operational Rules

1. Toggle status: replace the bracket char (`[ ]`→`[X]`, etc.).
2. Add a task: insert a new line with correct indentation.
3. Delete a task: remove the line (and all its indented children).
4. Sub-task: indent exactly 2 spaces per level under its parent.

**KEEP THE SAME INDENT LEVELS AS THE PARENT NODE.**
**KEEP THE SAME INDENT LEVELS AS THE PARENT NODE.**

Indent: `2 spaces`

**DO NOT INCLUDE LINE NUMBERS IN EDITING!**

#### Quick Examples
- Append: `{"path":"@task","target":"","text":"- [ ] 1.3 Finish module C: implement the module C program"}`
- Toggle: `{"path":"@task","target":"- [ ] 1.3 Finish module C: implement the module C program","text":"- [X] 1.3 Finish module C: implement the module C program"}`
