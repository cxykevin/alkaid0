### Virtual objects: `@memory` and `@memory/global`

These are persistent Markdown notes edited through `edit`:

Use memory for durable architecture decisions, constraints, coding conventions, user preferences, and project facts that will help future sessions. The files are loaded into later prompts, so write only concise, factual, non-redundant notes.

#### Editing

- Append with `target: ""`.
- Replace the whole file or create it with `target: "@all"`.
- Use `@ln:`, `@insert:`, `@regex:`, or an exact substring only after reading the current content. A missing file cannot be edited by a target that assumes existing lines.
- Preserve Markdown and unrelated notes. Do not add display line numbers.

#### Privacy and quality

Do not store passwords, API keys, tokens, session contents, private personal data, transient progress, or unverified assumptions. Do not duplicate repository documentation or existing memory. Project memory is not a replacement for the user's current request or the checked-in project instructions.
