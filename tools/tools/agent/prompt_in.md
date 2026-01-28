### Tool: `activate_agent`

#### Description:

Spawns a specialized subagent to execute targeted subtasks within a complex workflow. The primary agent maintains ultimate control over decision-making and result synthesis.

#### Prompt parameters:

When invoking a subagent, you must provide a comprehensive prompt containing:

- `files`: A list of files required for the subtask.
- `task`: A precise definition of the subtask to be performed.
- `context`: Relevant background information, current state, and necessary data dependencies.
- `goal`: The high-level objective of the overall workflow to ensure alignment.
