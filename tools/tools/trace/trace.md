### Traced Files

The following are snippets of files currently being **traced**. These files are identified as the core context for the current task. You must prioritize the implementation details, data structures, and logic found within these files over any general knowledge or assumptions.

#### Critical Instructions

1.  **Source of Truth**: Treat these files as the primary reference for the project's coding style, architectural patterns, and business logic.
2.  **Line Numbering & Referencing**: Each line in the `<content>` block is prefixed with its absolute line number (e.g., ` 10 | code`). When suggesting edits, explaining logic, or identifying bugs, you **MUST** refer to these specific line numbers. However, **DO NOT** include these line numbers in any code you generate or modify. 

**Line number IS NOT A PART OF FILE, DO NOT contains the line number in anywhere you OUTPUT**
**Line number IS NOT A PART OF FILE, DO NOT contains the line number in anywhere you OUTPUT**
**Line number IS NOT A PART OF FILE, DO NOT contains the line number in anywhere you OUTPUT**

**DO NOT INCLUDE LINE NUMBERS IN EDITING!**
**DO NOT INCLUDE LINE NUMBERS IN EDITING!**
**DO NOT INCLUDE LINE NUMBERS IN EDITING!**

3.  **Handling Discontinuous Snippets**: 
    *   Be aware that the provided content may consist of **one or more non-contiguous fragments** rather than the full file.
    *   Use the line numbers to determine the relative positioning and distance between code blocks.
    *   If you encounter a jump in line numbers, assume there is omitted code in between. Do not hallucinate missing logic; instead, request the missing range if it is critical to your understanding.
4.  **Contextual Awareness**: If the traced snippets conflict with previous information, the traced content takes precedence.
5.  **Incremental Retrieval**: If the provided snippets are insufficient to complete the task or if you suspect a side effect in an omitted section, use the `trace` tools to read the necessary line ranges.

#### Files Content

<tracedFiles>
{{range .}}
    <file path="{{.Name}}" size="{{.Size}}" linecount="{{(string .Length)}}"><![CDATA[
{{.Text}}
]]></file>
{{end}}
</tracedFiles>