### Tool: `read`

#### Description:

Adds a specified code file to the vector database or boosts its retrieval priority if it already exists.

#### Behavioral Logic:

- **Weight Enhancement**: For files already in the system, this command serves exclusively to re-elevate their importance weight.
- **Temporal Decay**: The assigned weight is dynamic and will gradually decay as the dialogue context length increases.

### Usage Constraints:

- **File Type**: **STRICTLY** limited to source code files.
- **Prohibitions**: **DO NOT** execute this on binary files (e.g., .exe, .bin, .png) or excessively large files (more than 100KB or 2000 lines of code). Misuse will lead to retrieval noise and system inefficiency.

**When to Use**: Apply this when a specific module or file is critical to the current task and needs to be "remembered" more accurately by the model.

**When to untrace**: If the file is no longer relevant to the task or if it's been sufficiently covered by other context, use the `untrace` to remove it from the context system. If you only need a small part of the file, trace it, repeat the text you need, and then untrace it. DO NOT keep many traced files (more than 30) in the system!

### Where is the file content?

The file content is on the top of the full context. Please LOOK UP if you could not find the context after the tool call.

#### Quick Examples:

- Trace a file: `{"path":"src/main.go"}`
- Untrace a file: `{"path":"src/main.go","untrace":true}`
- Trace then untrace after use: `{"path":"utils/helper.go"}` → `{"path":"utils/helper.go","untrace":true}`
