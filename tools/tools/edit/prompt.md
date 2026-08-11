### Tool: `edit`

#### Description:

Edit or create file or virtual objects. To trigger LSP diagnostics on a file without making changes, use `target: ""` and `text: ""` (empty values) — the tool will run LSP formatting and syntax checks and return the results in `diagnostics` without modifying the file.

#### Target parameters (determines where and how to edit):

- `""` (Empty string): **Append text to the end** of the file.
- `@all`: **Replace the entire** file content (creates the file if it does not exist).
- `@insert:{line}`: **Insert text above** line {line}.
- `@ln:{from}-{to}`: **Replace the content** from line {from} to line {to} (inclusive).
- `@regex:/{pattern}/{flag}`: **Replace {pattern}** matching the regex.
    - Flags: `g` (replace all occurrences), `i` (case insensitive).
- A specific substring: **Replace the first occurrence** of this substring.

#### Notes:

- A space is automatically added at the end of the inserted text.
- If file does not exist, always using `@all` instead of other targets.
- **Post-edit Auto Formatting & Diagnostics**: After a successful edit, the system automatically checks the file for syntax issues. The result may include:
  - `format_applied: true` — the file was auto-formatted (only for LSP-supported languages).
  - `diagnostics: [...]` — a list of syntax errors and warnings found (line numbers are 1-based). Check these and fix any issues in subsequent edits.
  - **LSP languages** (Go, Python, Rust, C/C++, Java, Kotlin, C#, JS/TS, Vue): formatting + diagnostics via language server.
  - **Native syntax check** (JSON5, JSONL, YAML, TOML, INI, Markdown): parsed directly for syntax errors. Markdown checks for unclosed code fences.
- **Editing `@tree` (virtual file tree)**: Use `path: "@tree"` to edit the workspace file tree structure:
  - Copy: add a new indented line with the same backticked ID as the source.
  - Delete: remove the line (and indented children) for the entry to delete.
  - Move/Rename: change the name text or move the line while keeping the backticked ID.
  - Indent with exactly **4 spaces** per level. New file entries must be followed by an actual file edit.
  - **DO NOT** expand collapsed directories (`... (N files)`).
- **Editing `@task` (virtual task plan)**: Use `path: "@task"` to edit the task plan list:
  - Each line: `- [X] taskName: taskDetails` (only `-` bullet, no `*`/`+`/numbers).
  - Status: `[ ]` (waiting), `[-]` (doing), `[X]` (done).
  - Nesting: exactly **2 spaces** per level. `taskName`/`taskDetails` are separated by the first `:`.
#### Quick Examples:

- Fix a bug: `{"path":"src/main.go","target":"@ln:42","text":"return result, nil"}`
- Create a new file: `{"path":"README.md","target":"@all","text":"# My Project\n\nDocs here."}`
- Append a line: `{"path":".gitignore","target":"","text":"node_modules/\n*.log"}`
- Regex replace all: `{"path":"config.json","target":"@regex:/localhost/g","text":"127.0.0.1"}`
- Trigger diagnostics only: `{"path":"main.go","target":"","text":""}`
- Update task plan: `{"path":"@task","target":"- [ ] fix bug","text":"- [X] fix bug"}`
- Move a file in tree: `{"path":"@tree","target":"- old.go `1`","text":"- new.go `1`"}`
- Save project memory: `{"path":"@memory","target":"","text":"- Key decision: use SQLite for storage."}`
