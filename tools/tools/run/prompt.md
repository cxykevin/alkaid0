### Tool: `run`

#### Description:

Start a task to run something likes code, command, and so on.

#### Parameters:

- `type` (required): A Enum decided which type of task want to do. Must Be First Parameter. Enum: ["shell", "sleep"]
- `reason` (required): A short(<=20 words) reason of this task. Must Be Second Parameter.
- `command` (required): Command or program will be run. For `"sleep"` type, it must be an int number representing seconds to wait. Must Be Third Parameter.
- `timeout` (optional): Timeout of the command. Default is 60(seconds). If it will not be run in background(default), it must less than 300(seconds). Only available in `"shell"` type.
- `sandbox` (optional): Whether run in sandbox. Some type don't support this parameter. Default is true. Only available in `"shell"` type.

#### Type parameters (which type of task you want to run):

- `"shell"`: Start a system command.
- `"sleep"`: Wait for specified seconds. `command` must be an int number (seconds). No command is executed.

#### Default shell of `"shell"` type in different OS:

- `"bash"`: Linux.
- `"zsh"`: MacOS.
- `"powershell"`: Windows.

#### Quick Examples:

- Build project: `{"type":"shell","reason":"build project","command":"go build ./..."}`
- Run tests: `{"type":"shell","reason":"run tests","command":"go test ./...","timeout":120}`
- Quick command no sandbox: `{"type":"shell","reason":"check disk","command":"df -h","sandbox":false}`
- Wait 30 seconds: `{"type":"sleep","reason":"wait for service to start","command":30}`
- Wait 5 seconds: `{"type":"sleep","reason":"wait for file to be created","command":5}`
