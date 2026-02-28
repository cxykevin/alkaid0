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

##### Example Task:

###### Task One:

*   **User Request:** Read the content of `helloworld.py` and print it.

*   **Current State in Context:**
    (File not in traced files)

*   **Your Action:**
    1. Using `trace` to get the content of `helloworld.py`.
    
*   **Your Output:**

<tools>
[
    {
        "name": "trace",
        "id": "trace_helloworld",
        "path": "helloworld.py"
    }
]
</tools>

*   **Resulting State:**
    ```
    <tracedFiles>
        <file path="helloworld.py" size="..." linecount="..."><![CDATA[
    helloworld! There are something of the file content.
    ]]></file>
    </tracedFiles>
    ```

###### Task Two:

*   **User Request:** Replace the output logic in `main.cpp` to output helloworld.

*   **Current State in Context:**
    (File not in traced files)
    ```
    1|#include <iostream>
    2|using namespace std;
    3|func main() {
    4|    int a, b;
    5|    cin >> a >> b;
    6|    cout << a + b << endl;
    7|    return 0;
    8|}
    9|
    ```

*   **Your Action:**
    1. Using `trace` to get the content of `helloworld.py`.
    2. Using the edit tool replacing full of old logic.
    3. Using `trace` and set `untrace=true` to free context resources.
    
*   **Your Output:**

<tools>
[
    {
        "name": "trace",
        "id": "get_algorithm",
        "parameters": {
            "path": "main.cpp"
        }
    }
]
</tools>

======

<tools>
[
    {
        "name": "edit",
        "id": "edit_algorithm",
        ...
    }
]
</tools>

======

<tools>
[
    {
        "name": "trace",
        "id": "free_context",
        "parameters": {
            "path": "main.cpp",
            "untrace": true
        }
    }
]
</tools>

*   **Resulting State:**
    (No files in traced files)
    ```
    1|#include <iostream>
    2|using namespace std;
    3|func main() {
    4|    cout << "hello world" << endl;
    5|    return 0;
    6|}
    7|
    ```
