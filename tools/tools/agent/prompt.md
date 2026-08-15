### Tool: `agent`

Manage named subagent instances: create or update an instance, or delete it. This tool does not execute the subagent's task; use `activate_agent` after creation to start work, and use `deactivate_agent` to request its final report.

#### Parameters

- `name` (string, required): Exact instance name.
- `tag` (string, optional): Agent configuration/tag. Required when creating or updating an instance; choose one listed in `<agent_tags>`.
- `path` (string, optional): Relative workspace path bound to the instance. Required when creating or updating; keep it within the current workspace and limit the agent to the smallest needed area.
- `delete` (boolean, optional, default `false`): When `true`, delete the named instance instead of creating or updating it. Do not delete an active instance unless its work is complete or cancellation is intended.

An existing name is updated; a new name is created. Use the exact names, tags, and paths shown in the `<agents>` and `<agent_tags>` context. Do not invent a tag or assume that an agent can edit outside its bound path.

#### Lifecycle

1. Create or update the instance with a focused role, tag, and relative path.
2. Call `activate_agent` with a precise task, scope, constraints, and expected report.
3. Wait for completion or a deactivation signal; independently inspect important changes and test results.
4. Ask the agent to `deactivate_agent` with a concise final report, then delete the instance when it is no longer needed.

#### Examples

- Create: `{"name":"code_reviewer","tag":"fast","path":"src"}`
- Update: `{"name":"code_reviewer","tag":"fast","path":"src/review"}`
- Delete after completion: `{"name":"code_reviewer","delete":true}`
