### Tool: `deactivate_agent`

End the currently active subagent assignment and return a final report to the parent agent. This is the final lifecycle step after `activate_agent` completes, fails, or is intentionally stopped.

#### Parameters

- `prompt` (string, required): A concise factual report. State the result, files changed or inspected, verification actually run and its outcome, errors or blockers, and any follow-up needed. If the task failed, say why and do not claim partial work is complete.

The report is communication to the parent agent, not a user-facing answer. Do not include credentials, private data, or unsupported conclusions.

#### Examples

- Success: `{"prompt":"Review completed. Inspected src/auth/login.go; no files changed. go test ./src/auth passed. No blockers."}`
- Failure: `{"prompt":"Task failed: the required test service was unavailable. No files were modified; verification was not run."}`
