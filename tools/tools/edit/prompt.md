Edit or create file or virtual objects.

### Target parameters (determines where and how to edit):

- `""` (Empty string): Append text to the end of the file.
- `@all`: Replace the entire file content (creates the file if it does not exist).
- `@ln:{line}`: Insert text below line {line}.
- `@ln:{from}-{to}`: Replace the content from line {from} to line {to} (inclusive).
- `@regex:/{pattern}/{flag}`: Replace {pattern} matching the regex.
    - Flags: `g` (replace all occurrences), `i` (case insensitive).
- A specific substring: Replace the first occurrence of this substring.

### Notes:

- A space is automatically added at the end of the inserted text.
