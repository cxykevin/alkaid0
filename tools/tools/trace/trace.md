### Read Files

The following snippets are files currently included in the read context. These files are identified as the core context for the current task. You must prioritize the implementation details, data structures, and logic found within these files over general knowledge or assumptions.

#### Critical Instructions

1. **Source of Truth**: Treat these files as the primary reference for the project's coding style, architectural patterns, and business logic.
2. **Line Numbering & Referencing**: Each line in the `<content>` block is prefixed with its absolute line number (for example, ` 10 | code`). When suggesting edits, explaining logic, or identifying bugs, refer to these specific line numbers. Do not include these line numbers in code you generate or modify.
3. **Handling Discontinuous Snippets**: The provided content may consist of one or more non-contiguous fragments rather than the full file. A jump in line numbers means code is omitted; do not hallucinate missing logic. Request the missing range if it is critical.
4. **Contextual Awareness**: If the read snippets conflict with previous information, the read content takes precedence.
5. **Incremental Retrieval**: If the snippets are insufficient to complete the task or you suspect a side effect in an omitted section, use the `read` tool to inspect the necessary line ranges.
6. **DO NOT INCLUDE LINE NUMBERS IN EDITING**: Line number IS NOT A PART OF FILE, DO NOT contains the line number in anywhere you OUTPUT
#### Files Content

<readFiles>
{{range .}}
    <file path="{{.Name}}" size="{{.Size}}" linecount="{{(string .Length)}}"{{if .Type}} type="{{.Type}}"{{end}}><![CDATA[
{{.Text}}
]]></file>
{{end}}
</readFiles>
