### Tool: `deactivate_agent`

#### Description:

Terminate the subagent session and return results to the parent agent. Required final step for every subagent activation.

#### Parameters:

- `prompt` (required): Final output, summary, or error report to return to parent agent
#### Quick Examples:

- Report success: `{"prompt":"Security scan completed. Found 3 vulnerabilities (all P1). Details in /src/auth/scan-report.md"}`
- Report failure: `{"prompt":"Task failed: model API timeout after 3 retries. No files were modified."}`
