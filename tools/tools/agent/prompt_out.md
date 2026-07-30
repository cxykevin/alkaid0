### Tool: `deactivate_agent`

#### Description:

Terminate the subagent session and return results to the parent agent. Required final step for every subagent activation.

#### Parameters:

- `prompt` (required): Final output, summary, or error report to return to parent agent

#### Quick Examples:

- Success: `{"prompt":"Task completed. Found 2 high-severity issues in login.js, 3 medium issues. Full report attached."}`
- Error: `{"prompt":"Error: Cannot access /src/auth directory. Permission denied. Suggest checking directory permissions or using a different workspace path."}`
