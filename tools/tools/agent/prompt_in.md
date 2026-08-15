### Tool: `activate_agent`

Activate one existing subagent instance and assign it a focused task. The instance must already exist in the `<agents>` list; use `agent` first if it does not.

#### Parameters

- `name` (string, required): Exact name of the existing instance.
- `prompt` (string, required): Complete task instructions, including objective, allowed scope, constraints, relevant context, expected artifacts, and verification requirements.

Before activation, confirm the selected tag and bound path are appropriate. Keep the task narrow and do not delegate work that requires permissions or tools the instance does not have. The subagent communicates only with the parent agent, not directly with the user.

Activation is not completion. After the subagent finishes, obtain its result through `deactivate_agent` and treat the report as evidence to verify, not as proof that changes or tests succeeded.

#### Example

`{"name":"security_scanner","prompt":"Inspect src/auth for input-validation bugs. Do not modify files. Return file paths, line numbers, reproduction conditions, and unresolved uncertainty."}`
