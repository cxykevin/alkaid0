### Tool: `scope`

Enable or disable a named tool scope for the current session. The available scope names are listed in the `<scopes>` block of the system prompt; use the exact name shown there.

#### Parameters

- `name` (string, required): Exact non-empty scope name.
- `disable` (boolean, optional, default `false`): `false` enables the scope; `true` disables it.

The default empty scope (`""`) is always enabled and cannot be changed. Do not guess scope names or attempt to change an unknown scope. Disabling a scope removes its tools from future context; only disable it when they are no longer needed for the remainder of the project or session. A failed call leaves the current scope state unchanged.

#### Skills

Skills are listed as `skill.<name>` scopes (Agent Skills: a directory containing `SKILL.md`). Enabling one links the skill directory, read-only, to `.alkaid0/skills/<name>` in the workspace and loads `.alkaid0/skills/<name>/SKILL.md` into your read context automatically; follow its instructions, including any resources it ships with (read them from that directory relative to the workspace root). Disabling removes that link and every traced file under the skill directory. Skill directories are read-only and are never modified by enabling or disabling.

#### Examples

- Enable a scope: `{"name":"codebase","disable":false}`
- Disable a scope: `{"name":"codebase","disable":true}`
- Inspect the current `<scopes>` block before choosing a name; this tool does not list scopes itself.
