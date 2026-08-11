### Tool: `run`

#### Description:

Start a task to run something likes code, command, and so on.

#### Parameters:

- `type` (required): A Enum decided which type of task want to do. Must Be First Parameter. Enum: ["shell", "sleep", "wait"]
- `reason` (required): A short(<=20 words) reason of this task. Must Be Second Parameter.
- `command` (required): Command or program will be run. For `"sleep"` type, it must be an int number representing seconds to wait. For `"wait"` type, it must be the run id returned by a background run. Must Be Third Parameter.
- `timeout` (optional): Timeout of the command. Default is 60(seconds). If it will not be run in background(default), it must less than 300(seconds). If run in background, default is no timeout and no limit. Only available in `"shell"` type.
- `sandbox` (optional): Whether run in sandbox. Some type don't support this parameter. Default is true. Only available in `"shell"` type.
- `background` (optional): Whether run in background. Default is false. Only available in `"shell"` type. If true, the command runs asynchronously: a temp object is created immediately and its path returned as `run_id`, the temp object is updated every 60 seconds until the command finishes, and updated once more with the final output.

#### Type parameters (which type of task you want to run):

- `"shell"`: Start a system command.
- `"sleep"`: Wait for specified seconds. `command` must be an int number (seconds). No command is executed.
- `"wait"`: Block until a background run finishes. `command` must be the `run_id` returned by a previous background run. The background task keeps running; you can also read the `run_id` temp object with `trace` at any time to check progress.

#### Background usage:

For a long-running command, set `"background":true`. The tool returns immediately with `"run_id"` (the temp object path). Use `{"type":"wait","command":"<run_id>"}` to block until it finishes, or `trace` the `run_id` path to inspect current status/output at any time. Background commands are not bound to the session: they survive session stop and run until timeout or completion.

#### Default shell of `"shell"` type in different OS:

- `"bash"`: Linux.
- `"zsh"`: MacOS.
- `"powershell"`: Windows.

#### Rules:

DO NOT use the `run` tools or bash cmds to perform ANY tasks that belong to other tools!!!
DO NOT use the `run` tools or bash cmds to perform ANY tasks that belong to other tools!!!
DO NOT use the `run` tools or bash cmds to perform ANY tasks that belong to other tools!!!

NEVER read file by `run` tools, instead, use `trace` tools!
NEVER write file by `run` tools, instead, use `edit` tools!
NEVER fetch page by `run` tools, instead, use `fetch` tools!

#### Quick Examples:

- Run a command: `{"type":"shell","reason":"check git status","command":"git status"}`
- Sleep briefly: `{"type":"sleep","reason":"wait for build to finish","command":"5"}`
- Wait for background job: `{"type":"wait","reason":"wait for server start","command":"run/run-20260101-120000"}`
- Run in background: `{"type":"shell","reason":"start dev server","command":"go run .","background":true}`
- Disable sandbox: `{"type":"shell","reason":"list workspace files","command":"ls -la","sandbox":false}`
- Custom timeout: `{"type":"shell","reason":"run slow tests","command":"go test -v ./...","timeout":120}`
