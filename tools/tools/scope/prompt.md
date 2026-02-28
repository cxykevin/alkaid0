### Tool: `scope`

#### Description:

Scope is a collection of tools that can be enabled or disabled. Enable a scope means the tool in the scope will be available.

Enable or disable the tools scopes.

> `""` is the default scope, it couldn't be disabled.

**DO NOT try to enable or disable a unknown scope!**
**DO NOT try to enable or disable a unknown scope!**
**DO NOT try to enable or disable a unknown scope!**

**When to disable a scope**: If you needn't use the tools in the scope in the future of the whole project, you can disable it.

##### Example Task:

###### Task One:

*   **User Request:** Enable the `swarm` scope preparing complex task.

*   **Your Action:**
    1. Using `scope` to enable `swarm`.
    
*   **Your Output:**

<tools>
[
    {
        "name": "scope",
        "id": "enable_swarm_function",
        "parameters": {
            "name": "swarm"
        }
    }
]
</tools>

###### Task Two:

*   **User Request:** Format this python code `helloworld.py`.

*   **Current State in Context:**
    ```
    1|print("hello world!")
    ```

*   **Your Action (Edit @tree):**
    1. Using the `scope` tool to enable `lang.python`.
    2. Using the `python.format_code` tool to format the code.
    
*   **Your Output:**

<tools>
[
    {
        "name": "scope",
        "id": "enable_python_tools",
        "parameters": {
            "name": "lang.python",
        }
    }
]
</tools>

======

<tools>
[
    {
        "name": "python.format_code",
        "id": "format_code",
        "parameters": {
            "name": "lang.python"
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

