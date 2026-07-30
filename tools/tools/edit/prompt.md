### Tool: `edit`

#### Description:

Edit or create file or virtual objects. To trigger LSP diagnostics on a file without making changes, use `target: ""` and `text: ""` (empty values) — the tool will run LSP formatting and syntax checks and return the results in `diagnostics` without modifying the file.

#### Target parameters (determines where and how to edit):

- `""` (Empty string): **Append text to the end** of the file.
- `@all`: **Replace the entire** file content (creates the file if it does not exist).
- `@insert:{line}`: **Insert text above** line {line}.
- `@ln:{from}-{to}`: **Replace the content** from line {from} to line {to} (inclusive).
- `@regex:/{pattern}/{flag}`: **Replace {pattern}** matching the regex.
    - Flags: `g` (replace all occurrences), `i` (case insensitive).
- A specific substring: **Replace the first occurrence** of this substring.

#### Notes:

- A space is automatically added at the end of the inserted text.
- If file does not exist, always using `@all` instead of other targets.
- **Post-edit Auto Formatting & Diagnostics**: After a successful edit, the system automatically checks the file for syntax issues. The result may include:
  - `format_applied: true` — the file was auto-formatted (only for LSP-supported languages).
  - `diagnostics: [...]` — a list of syntax errors and warnings found (line numbers are 1-based). Check these and fix any issues in subsequent edits.
  - **LSP languages** (Go, Python, Rust, C/C++, Java, Kotlin, C#, JS/TS, Vue): formatting + diagnostics via language server.
  - **Native syntax check** (JSON5, JSONL, YAML, TOML, INI, Markdown): parsed directly for syntax errors. Markdown checks for unclosed code fences.

##### Example Task:

###### Task One:

*   **User Request:** Create a simple `helloworld.py`

*   **Current State in Context:**
    (File not found)

*   **Your Action (Edit @tree):**
    1. Using `@all` to create the entire file content.
    
*   **Your Output:**

<tools>
[
    {
        "name": "edit",
        "id": "edit_helloworld",
        "parameters": {
            "path": "helloworld.py",
            "target": "@all",
            "text": "print('hello world')\n"
        }
    }
]
</tools>

*   **Resulting State:**
    ```
    1|print('hello world')
    2|
    ```

###### Task Two:

*   **User Request:** Replace the output logic in `main.cpp` to output helloworld.

*   **Current State in Context:**
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

*   **Your Action (Edit @tree):**
    1. Using the edit tool replacing full of old logic.
    
*   **Your Output:**

<tools>
[
    {
        "name": "edit",
        "id": "edit_algorithm",
        "parameters": {
            "path": "main.cpp",
            "target": "int a, b;\n    cin >> a >> b;\n    cout << a + b",
            "text": "cout << \"hello world\""
        }
    }
]
</tools>

*   **Resulting State:**
    ```
    1|#include <iostream>
    2|using namespace std;
    3|func main() {
    4|    cout << "hello world" << endl;
    5|    return 0;
    6|}
    7|
    ```
