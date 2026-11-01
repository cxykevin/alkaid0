### Tool: `file`

Move, copy, link or mount a file or directory. For `move`, `copy` and `link`, `src` and `dst` are workspace-relative; `..`, absolute paths, globs and symlink escapes are rejected.

#### Parameters

- `type` (string, required): `"move"`, `"copy"`, `"link"` or `"mount"`.
- `src` (string, required): Source path, or a virtual object such as `@task`, `@memory`, `@memory/global`, `@tree`, `@cron`, `@agents`, `@docs/...` or `@history/{session_id}`. When `src` is a virtual object, its current text content is written to `dst` (move and copy behave the same; there is nothing to delete). For `mount`, `src` must be an absolute directory path (`/home/user/data`, `C:\data`) and describes the directory to expose inside the workspace.
- `dst` (string, required): Destination path. For `move` only, `@trash` deletes `src`. `@temp/run/<id>` writes the entire source file to that running terminal's stdin without sending EOF. `dst` cannot be a virtual object. For `mount`, `dst` may only contain letters, digits and underscores.

#### Semantics

- `copy` uses copy-on-write cloning for large files when the filesystem supports it (FICLONE / clonefile / DuplicateExtents); otherwise it falls back to a normal copy and reports progress. Directories are copied recursively.
- `move` renames the entry; when rename is not possible (for example across filesystems) it falls back to copy + delete.
- `link` creates a symbolic link at `dst` pointing to `src`. Both must be real paths: `@trash`, terminals and virtual objects are not allowed, and `dst` must not exist.
- `mount` behaves like `link`, but for a directory outside the workspace: it creates the symbolic link `dst + ".alkmount"` in the **workspace root** pointing to the absolute `src`, and registers the target in the workspace mount table (`.alkaid0/mounts.json`). Sandboxed commands get read-write access to every registered target, so a mount is the supported way to work on outside data. The target must be an existing directory.
- `mount` may be called again with the same `dst` to overwrite an existing mount link (for example to repair a stale mount or point it at another directory). Only symbolic links are replaced; a regular file or directory with that name is never touched and the call fails.
- To unmount, `move` the link to `@trash` (for example `{"type":"move","src":"data.alkmount","dst":"@trash"}`). This deletes the link and removes the entry from the mount table. Deleting a plain file that merely looks like a mount link does not unbind anything.
- The mount table is self-healing: when the workspace tree is scanned, entries whose link is missing, points somewhere else, or whose target directory is unreachable are removed together with their links. Scanning never creates new mounts.

Overwriting an existing file is allowed for `move` and `copy`; copying or moving onto an existing directory is rejected.

#### Examples

- Copy a file: `{"type":"copy","src":"src/main.go","dst":"src/main.go.bak"}`
- Move a directory: `{"type":"move","src":"build","dst":"dist"}`
- Delete: `{"type":"move","src":"old.log","dst":"@trash"}`
- Send a file to a running terminal: `{"type":"copy","src":"logs/output.txt","dst":"@temp/run/1"}`
- Write @task to a file: `{"type":"copy","src":"@task","dst":"TASK.md"}`
- Symlink: `{"type":"link","src":"node_modules/pkg","dst":"pkg"}`
- Mount an outside directory as `data.alkmount`: `{"type":"mount","src":"/mnt/datasets","dst":"data"}`
