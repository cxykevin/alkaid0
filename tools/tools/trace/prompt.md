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
