### Tool: `file`

Move, copy or link a file or directory inside the workspace. `src` and `dst` are workspace-relative; `..`, absolute paths, globs and symlink escapes are rejected.

#### Parameters

- `type` (string, required): `"move"`, `"copy"` or `"link"`.
- `src` (string, required): Source path, or a virtual object such as `@task`, `@memory`, `@memory/global`, `@tree`, `@cron`, `@agents`, `@docs/...` or `@history/{session_id}`. When `src` is a virtual object, its current text content is written to `dst` (move and copy behave the same; there is nothing to delete).
- `dst` (string, required): Destination path. For `move` only, `@trash` deletes `src`. `@temp/run/<id>` writes the entire source file to that running terminal's stdin without sending EOF. `dst` cannot be a virtual object.

#### Semantics

- `copy` uses copy-on-write cloning for large files when the filesystem supports it (FICLONE / clonefile / DuplicateExtents); otherwise it falls back to a normal copy and reports progress. Directories are copied recursively.
- `move` renames the entry; when rename is not possible (for example across filesystems) it falls back to copy + delete.
- `link` creates a symbolic link at `dst` pointing to `src`. Both must be real paths: `@trash`, terminals and virtual objects are not allowed, and `dst` must not exist.

Overwriting an existing file is allowed for `move` and `copy`; copying or moving onto an existing directory is rejected.

#### Examples

- Copy a file: `{"type":"copy","src":"src/main.go","dst":"src/main.go.bak"}`
- Move a directory: `{"type":"move","src":"build","dst":"dist"}`
- Delete: `{"type":"move","src":"old.log","dst":"@trash"}`
- Send a file to a running terminal: `{"type":"copy","src":"logs/output.txt","dst":"@temp/run/1"}`
- Write @task to a file: `{"type":"copy","src":"@task","dst":"TASK.md"}`
- Symlink: `{"type":"link","src":"node_modules/pkg","dst":"pkg"}`
