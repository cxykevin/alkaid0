#### The Operation of the virtual object `@tree`

###### Operational Logic:

1. **Copying (Cloning):** To copy a file or entry, create a new line in the target location and **reuse the exact same ID** from the source. Identical IDs indicate multiple references to the same physical content.
2. **Deleting:** To delete a file or directory, simply **remove the corresponding line** (and all its indented children).
3. **Moving/Renaming:** Change the name text or relocate the line while keeping the backticked ID unchanged.

###### Performance & Safety Constraints:

1. **Large Directory Protection:** If a directory contains a summary like `... (N files)` (e.g., `node_modules`), **DO NOT** attempt to expand, edit, or add files within it. These are collapsed for performance reasons. You may only delete the entire directory line if requested.
2. **Structural Integrity:** Ensure all parent-child relationships are maintained through correct indentation. A single missing space can break the tree logic.
3. **New File:** You must edit the file instead of adding a new file line with unused ID. 

**YOU MUST KEEP THE SAME INDENT LEVELS AS THE PARENT NODE.**
**YOU MUST KEEP THE SAME INDENT LEVELS AS THE PARENT NODE.**
**YOU MUST KEEP THE SAME INDENT LEVELS AS THE PARENT NODE.**

Indent: `4 spaces`

###### Example Task:

*   **User Request:** "Copy `bar` from `foo` to `hello` as `bar_copy`, then delete `world`."

*   **Current State in Context:**
    ```
    foo
        - bar `1`
    hello
        - world `2`
    ```

*   **Your Action (Edit @tree):**
    1. Add `- bar_copy '1'` under the `hello` directory.
    2. Remove the line `- world '2'`.

*   **Resulting State:**
    ```
    foo
        - bar `1`
    hello
        - bar_copy `1`
    ```
