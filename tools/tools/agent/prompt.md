### Tool: `agent`

#### Description:

Manage subagent lifecycle: create, configure, and delete isolated agent instances.

Each subagent has its own:
- **Prompt template**: Independent instructions and behavior patterns
- **Model configuration**: Separate model and settings  
- **Execution context**: Isolated state and session data
- **Workspace scope**: Optional path restriction (must be relative, within current directory)

Subagents inherit context from the parent agent session. They communicate exclusively with the parent agent, never directly with users.

#### Common workflow:

1. Create agent with `agent` tool (specify name, tag, optional path)
2. Activate it with `activate_agent` (provide name and task prompt)
3. Receive deactivation signal when task completes or on error
