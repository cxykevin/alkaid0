### Virtual object: `@tree`

`@tree` is a structural view of the current workspace, edited through the `edit` tool. It is not a real file and cannot be used to read arbitrary file contents.

#### Representation

- A directory is a plain name without an ID.
- A file or entry is `- name \`ID\``; the backticked ID points to its physical content.
- A collapsed directory is `... (N files)` and must not be expanded.
- Display line numbers are context metadata, not part of the tree text. Never copy them into an edit target or replacement text.

#### Operations

- **Copy**: insert a new entry at the desired location and reuse the exact source ID.
- **Delete**: remove the target entry and all indented descendants. Delete a collapsed directory only as a whole when requested.
- **Move or rename**: relocate or change the name while preserving the same ID.
- **Create**: add a new entry only together with a subsequent `edit` call that creates the real file; do not invent an unused ID.

Preserve hierarchy and use exactly 4 spaces for each nesting level. Make one minimal structural change at a time and check the `success`/`error` result. Do not expand collapsed content or use `@tree` as a substitute for editing file contents.

**YOU MUST KEEP THE SAME INDENT LEVELS AS THE PARENT NODE.**
Indent: `4 spaces`

#### Examples

- Copy an entry: `{"path":"@tree","target":"- app.go \`1\`","text":"    - app_copy.go \`1\`"}`
- Delete an entry by its displayed line: `{"path":"@tree","target":"@ln:4","text":""}`
- Rename an entry while retaining its ID: `{"path":"@tree","target":"- old.go \`1\`","text":"- new.go \`1\`"}`
