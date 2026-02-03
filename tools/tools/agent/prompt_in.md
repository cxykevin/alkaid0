### Tool: `activate_agent`

#### Description:

Activate a subagent instance to perform a specific task. The subagent will operate independently within its configured scope and workspace restrictions.

**When to use this tool:**
- Delegate complex or time-consuming tasks to a specialized subagent
- Perform parallel analysis on different parts of a problem
- Isolate tasks that require different expertise or tools
- Handle tasks that need workspace isolation

#### Parameters:

**Required:**
- `name`: Subagent instance name (must be created first with `agent` tool)
- `prompt`: Complete task instructions and context

#### How to write effective prompts:

Your prompt should be comprehensive and include:

1. **Clear task definition**: What exactly needs to be done
2. **Relevant context**: Background information, constraints, dependencies
3. **Expected output format**: How results should be structured
4. **Success criteria**: What constitutes successful completion
5. **Error handling**: What to do if the task cannot be completed

#### Example:

```
activate_agent(
  name="security_scanner",
  prompt="""
## Task
Analyze the /src/auth directory for security vulnerabilities.

## Context
This is a Node.js authentication system using JWT tokens.
Focus on: SQL injection, XSS, and authentication bypass.

## Output Format
Provide a JSON array with:
- file_path: string
- vulnerability_type: string
- severity: "high"|"medium"|"low"
- description: string
- suggested_fix: string

## Success Criteria
Identify all critical and high-severity issues.

## If Unable to Complete
If you cannot access files or analyze code, deactivate immediately
and report the specific limitation.
"""
)
```

#### Best practices:

- Be specific about requirements and constraints
- Include all necessary context upfront
- Define clear output expectations
- Specify what to do on errors or limitations
- Keep the subagent focused on one primary task
