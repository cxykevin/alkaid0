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

##### Example Task:

###### Task One:

*   **User Request:** Using `frontend` agent writing a simple `helloworld.html`

*   **Current State in Context:**
    (empty)

*   **Your Action (Edit @tree):**
    1. Using `agent` to create a new agent `frontend-dev1` with tag `frontend`.
    2. Activate the agent with task prompt `Write a simple helloworld.html`.
    
*   **Your Output:**

<tools>
[
    {
        "name": "agent",
        "id": "create_frontend_dev_agent",
        "parameters": {
            "name": "frontend-dev1",
            "tag": "frontend"
        }
    }
]
</tools>

======

<tools>
[
    {
        "name": "activate_agent",
        "id": "activate_frontend_dev_agent_write_helloworld",
        "parameters": {
            "name": "frontend-dev1",
            "prompt": "Write a simple helloworld.html"
        }
    }
]
</tools>

*   **Resulting State:**
    (from subagent) I wrote a simple helloworld.html successfully.
