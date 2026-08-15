### Tool: `run`

Use only the `run` tool for execution and timing; use `read` for file content, `edit` for file changes, `search` for code search, and `fetch` for HTTP requests.

#### Parameters

- `type` (string, required): One of `shell`, `sleep`, or `wait`.
- `reason` (string, required): A short reason for the operation (20 words or fewer).
- `command` (string, required): The shell command for `shell`, an integer number of seconds for `sleep`, or the `run_id` returned by a background `shell` for `wait`.
- `timeout` (number, optional): For `shell` only. Defaults to 60 seconds for foreground runs. Foreground values must be less than 300 seconds; a background run defaults to no timeout. A non-positive foreground value falls back to 60 seconds.
- `sandbox` (boolean, optional): For `shell` only; defaults to `true`. The effective setting can still be restricted by project configuration or platform support.
- `background` (boolean, optional): For `shell` only; defaults to `false`. When true, return immediately with a `run_id` and update its temporary result while the command runs.

#### Types

- `shell`: Execute the command in the configured shell and workspace. Use the smallest command that directly verifies or performs the requested execution.
- `sleep`: Wait for `command` seconds without executing a process. The maximum is 3600 seconds. Use it only when a real time delay is required, not to guess whether another task has finished.
- `wait`: Block until the background job identified by `command` finishes. The returned result identifies the same temporary output path; it does not start the job again.

#### Background jobs

Set `background: true` for a command that may outlive the current request. The tool returns a `run_id`/`@temp` path immediately. Use `wait` when you need a definitive completion or failure result; use `read` to inspect progress without waiting. Do not infer completion from elapsed time or repeat the same command. Background jobs may continue after the session stops and are governed by their timeout and process lifecycle.

#### Safety and scope

- Do not use `run` as a substitute for a dedicated tool.
- Review commands before execution. Avoid destructive or externally visible commands unless the user has authorized them; do not expose credentials in commands or output.
- Prefer sandboxed execution. Disable the sandbox only when the operation genuinely requires it and the authorization and environment make that appropriate.
- Treat stdout, stderr, exit status, and temporary output as evidence. A successful tool call does not imply the command itself succeeded; inspect the result and run follow-up verification when needed.

#### Quick examples

- Foreground command: `{"type":"shell","reason":"run package tests","command":"go test ./..."}`
- Delayed check: `{"type":"sleep","reason":"wait before retry","command":"5"}`
- Definitive background wait: `{"type":"wait","reason":"await build completion","command":"run/run-20260101-120000"}`
- Background server: `{"type":"shell","reason":"start development server","command":"go run .","background":true}`
