###### Virtual Object: `@memory`

The `@memory` and `@memory/global` are persistent markdown memory files, editable via the `edit` tool.

- `@memory` (project-level): saved to `.alkaid0/MEMORY.md` in the project root (git-ignored, not tracked).
- `@memory/global` (global): saved to `MEMORY.md` in the same directory as the alkaid0 config file.

Use them to store and recall:
- Architecture decisions and design constraints.
- Coding conventions and standards.
- User preferences and project-specific knowledge.
- Notes that must survive across sessions.

#### Operational Rules

1. Append a note: `path: "@memory"` (or `"@memory/global"`), `target: ""`, `text: "<new note>"`.
2. Replace the whole file: `target: "@all"` (also for creating a new memory file).
3. Modify a specific line/substring: use `@ln:`, `@insert:`, `@regex:`, or a substring target.
4. Keep notes concise, factual, and non-redundant. Do not duplicate what is already written.
5. When a memory file does not exist yet, use `target: "@all"` (or `""`), never `@ln:`/`@insert:` targeting lines above 1.

**DO NOT INCLUDE LINE NUMBERS IN EDITING!**

#### Quick Examples
- Append to project memory: `{"path":"@memory","target":"","text":"- The project uses pure-Go dependencies."}`
- Replace global memory: `{"path":"@memory/global","target":"@all","text":"- Always reply in Chinese."}`
- Update a substring: `{"path":"@memory","target":"pure-Go","text":"pure-Go (no cgo)"}`
