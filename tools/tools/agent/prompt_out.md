### Tool: `deactivate_agent`

#### Description:

Terminate the subagent session and return results to the parent agent. This is a required final step for every subagent activation.

**When to use this tool:**
- Task completion: When the assigned task is finished successfully
- Error conditions: When the task cannot be completed due to limitations
- Early termination: When the subagent determines it cannot fulfill the request

#### Parameters:

**Required:**
- `prompt`: Final output, summary, or error report to return to parent agent

#### What to include in your final prompt:

**For successful completion:**
- Summary of what was accomplished
- Key findings or results
- Any relevant data or analysis
- Notes about approach or methodology

**For errors or failures:**
- Clear explanation of what went wrong
- Specific limitations encountered
- Suggestions for alternative approaches
- Any partial results that may be useful

#### Examples:

Successful completion:
```
Task completed: Analyzed 12 files in /src/auth

Findings:
- 2 high-severity issues (SQL injection in login.js, XSS in profile.js)
- 3 medium-severity issues
- All issues documented with line numbers and fix suggestions

Full report available in JSON format as requested.
```

Error condition:
```
Error: Cannot complete security scan

Reason: Access denied to /src/auth directory

Suggested fix: Check directory permissions or activate agent
with a different workspace path.

Partial results: Successfully scanned 5 accessible files,
no issues found in those files.
```

#### Important protocol:

- **Always** call this tool to terminate your session
- Do not add explanations or apologies beyond the prompt content
- Return structured data if that was requested
- Be concise but complete in your summary
- The parent agent will handle user communication
