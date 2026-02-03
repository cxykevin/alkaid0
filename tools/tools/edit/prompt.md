### Tool: `edit`

#### Description:

Edit or create file or virtual objects.

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
        "path": "helloworld.py",
        "target": "@all",
        "text": "print('hello world')\n"
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
        "path": "main.cpp",
        "target": "int a, b;\n    cin >> a >> b;\n    cout << a + b",
        "text": "cout << \"hello world\""
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
