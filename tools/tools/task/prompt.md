### Virtual object: `@task`

`@task` is the current persistent task plan. Edit it through the `edit` tool with `path: "@task"`; it is not a shell file path.

#### Format

- Each task is one line: `- [ ] taskName: taskDetails`.
- Status is `[ ]` waiting, `[-]` doing, or `[X]` done.
- Use only the `-` bullet. Do not use `*`, `+`, or numbered lists.
- Separate the task name and details at the first `:`.
- Indent child tasks by exactly 2 spaces per level.

#### Editing rules

- Toggle only the intended status or text; preserve unrelated tasks, ordering, and indentation.
- Add a task as a new correctly indented line. Remove a task together with its indented children when the whole branch is deleted.
- Keep details factual and actionable. Do not record credentials, private data, or speculative work as completed.
- Do not include display line-number prefixes in the virtual object content.
- **DO NOT INCLUDE LINE NUMBERS IN EDITING!**
- 
Indent: `2 spaces`

Example: `{"path":"@task","target":"- [ ] run tests","text":"- [X] run tests"}`
