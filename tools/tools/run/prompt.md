### Tool: `run`

#### Description:

Start a task to run something likes code, command, and so on.

#### Parameters:

- `type` (required): A Enum decided which type of task want to do. Must Be First Parameter. Enum: ["shell"]
- `reason` (required): A short(<=20 words) reason of this task. Must Be Second Parameter.
- `command` (required): Command or program will be run. Must Be Third Parameter.
- `timeout` (optional): Timeout of the command. Default is 60(seconds). If it will not be run in background(default), it must less than 300(seconds).
- `sandbox` (optional): Whether run in sandbox. Some type don't support this parameter. Default is true.

#### Type parameters (which type of task you want to run):

- `"shell"`: Start a system command. 

#### Default shell of `"shell"` type in different OS:

- `"bash"`: Linux.
- `"zsh"`: MacOS.
- `"powershell"`: Windows.

#### Rules of `git commit`:

Unless **explicitly specified** by the user, your commit format must follow the conventions used in the user’s previous commits.

If the user has **no prior commit history**, or only an **Original Commit** exists, then the Conventional Commits specification shall be adopted by default.

You **must** ask for the user’s confirmation **only when** an Original Commit is present; in **all other cases**, no confirmation is required.

Every commit message must end with `Co-Authored-By: Alkaid0 <alkaid0@cxykevin.top>`.
