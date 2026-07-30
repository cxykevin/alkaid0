### Tool: `trace`

#### Description:

Adds a specified code file to the vector database or boosts its retrieval priority if it already exists. This ensures the file is treated as high-priority context for the RAG (Retrieval-Augmented Generation) system.

#### Behavioral Logic:

- **Weight Enhancement**: For files already in the system, this command serves exclusively to re-elevate their importance weight.
- **Temporal Decay**: The assigned weight is dynamic and will gradually decay as the dialogue context length increases.

### Usage Constraints:

- **File Type**: **STRICTLY** limited to source code files.
- **Prohibitions**: **DO NOT** execute this on binary files (e.g., .exe, .bin, .png) or excessively large files (more than 100KB or 2000 lines of code). Misuse will lead to retrieval noise and system inefficiency.

**When to Use**: Apply this when a specific module or file is critical to the current task and needs to be "remembered" more accurately by the model.

**When to untrace**: If the file is no longer relevant to the task or if it's been sufficiently covered by other context, use the `untrace` to remove it from the context system. If you only need a small part of the file, trace it, repeat the text you need, and then untrace it. DO NOT keep many traced files (more than 30) in the system!

#### Quick Examples:

- Trace file: `{"path":"main.cpp"}` → content appears in traced files
- Untrace: `{"path":"main.cpp","untrace":true}`
- Trace-read-untrace pattern: `{"path":"helper.go"}` → read → `{"path":"helper.go","untrace":true}`