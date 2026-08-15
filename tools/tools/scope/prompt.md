### Tool: `scope`

Enable or disable a named tool scope for the current session. The available scope names are listed in the `<scopes>` block of the system prompt; use the exact name shown there.

#### Parameters

- `name` (string, required): Exact non-empty scope name.
- `disable` (boolean, optional, default `false`): `false` enables the scope; `true` disables it.

The default empty scope (`""`) is always enabled and cannot be changed. Do not guess scope names or attempt to change an unknown scope. Disabling a scope removes its tools from future context; only disable it when they are no longer needed for the remainder of the project or session. A failed call leaves the current scope state unchanged.

#### Examples

- Enable a scope: `{"name":"codebase","disable":false}`
- Disable a scope: `{"name":"codebase","disable":true}`
- Inspect the current `<scopes>` block before choosing a name; this tool does not list scopes itself.
