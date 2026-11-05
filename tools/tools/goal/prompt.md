### Virtual object: `@goal`

`@goal` is the immutable session goal. Edit it through the `edit` tool with `path: "@goal"`; it is not a shell file path.

#### Rules

- The goal can be set **once**, and only while it is empty.
- Write the full text with `target: "@all"` (or `target: ""` when creating it).
- Maximum length: 1024 characters.
- After the goal is set, every edit fails. Only the user can replace or clear it with `/goal`.
- Keep it short, concrete, and verifiable: describe what must be true when the task is complete.
- Once set, the goal is injected at the top of the context. Keep working until it is satisfied; do not stop early.
