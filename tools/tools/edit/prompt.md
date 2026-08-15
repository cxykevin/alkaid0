### Tool: `edit`

Edit a workspace file or one of the supported virtual objects. Before changing an existing file, use `read` or another result that contains the current content; make the smallest targeted change that solves the request. After the edit, inspect `diagnostics` and fix reported issues before continuing.

#### Parameters

- `path` (string, required): A workspace-relative file path, or `@tree`, `@task`, `@memory`, or `@memory/global`. Do not use absolute paths, `..`, shell globs, or paths outside the workspace.
- `target` (string, required): Selects the edit operation below. It is a literal target, not a line-number comment.
- `text` (string, required): Replacement, inserted, or appended text. Preserve the file's existing format and newline style.

#### Target modes

- `""`: Append `text` to the file. With an existing file, the tool normalizes the final newline. With `text: ""`, leave the content unchanged and run LSP formatting/diagnostics.
- `"@all"`: Replace the entire file; also creates a missing file.
- `"@insert:{line}"`: Insert `text` before the 1-based line.
- `"@ln:{from}"` or `"@ln:{from}-{to}"`: Replace one line or an inclusive line range.
- `"@regex:/{pattern}/{flags}"`: Replace regex matches. `g` replaces all matches; without `g` only the first match is replaced; `i` enables case-insensitive matching.
- Any other string: Replace its first exact occurrence.

If a file does not exist, use `@all` or append mode; substring, regex, and out-of-range line edits cannot create a file. An edit target must match the current content exactly. Do not paste display line-number prefixes into `text`.

#### Automatic checks

After a successful file edit, the tool may format the file and returns:

- `format_applied: true` when LSP formatting changed the file.
- `diagnostics` containing syntax errors or warnings, with 1-based locations.

Treat diagnostics as actionable evidence. A successful write does not mean the file is correct; inspect the result and run relevant tests when the change is meaningful.

#### Virtual objects

- `@tree`: edit the workspace tree structurally. Files use ``- name `ID` ``; directories have no ID. Copy an entry by reusing its exact ID, delete an entry with its descendants, and move or rename it without changing its ID. Keep exactly 4 spaces per level. Do not expand `... (N files)` entries. Adding a new file entry is valid only when the corresponding real file is also created with `edit`.
- `@task`: edit the Markdown task list. Use only `- [ ]`, `- [-]`, or `- [X]`; separate the task name and details with the first `:`; use exactly 2 spaces per nesting level. Change only the intended task and preserve unrelated ordering.
- `@memory` and `@memory/global`: store durable project or global notes. Keep them concise, factual, non-sensitive, and non-redundant; never store credentials, tokens, private data, or transient conversational details.

#### Quick examples

- Replace a line: `{"path":"src/main.go","target":"@ln:42","text":"return result, nil"}`
- Create a file: `{"path":"README.md","target":"@all","text":"# Project\n\nDocs here."}`
- Append a note: `{"path":".gitignore","target":"","text":"node_modules/\n*.log"}`
- Replace all matches: `{"path":"config.json","target":"@regex:/localhost/g","text":"127.0.0.1"}`
- Diagnostics only: `{"path":"main.go","target":"","text":""}`
- Update a task: `{"path":"@task","target":"- [ ] fix bug","text":"- [X] fix bug"}`
