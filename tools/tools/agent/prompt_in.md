### Tool: `activate_agent`

#### Description:

Activate a subagent instance to perform a specific task. Use when delegating complex tasks, parallel analysis, or isolation needs.

#### Parameters:

- `name` (required): Subagent instance name (must be created first with `agent` tool)
- `prompt` (required): Complete task instructions and context

#### Quick Examples:

- Simple: `{"name":"security_scanner","prompt":"Scan /src/auth for SQL injection and XSS vulnerabilities"}`
- Structured output: `{"name":"data_analyzer","prompt":"Analyze logs in /var/log, return JSON array with {file, error_count, severity}"}`
