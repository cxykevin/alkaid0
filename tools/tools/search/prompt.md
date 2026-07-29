Search tool — search the codebase using AI rules (grep) and context engine (BM25 + vector).

## Parameters
- `query` (string, required): The search query. See "Query Modes" below for supported patterns.
- `online` (boolean, required): Whether to search online. If true, searches the internet via configured search engines (Bing/GitHub/arXiv/Tavily) and summarizes results via LLM.
- `path` (string, optional, default: workspace root): Search path (directory). Can be absolute or relative to workspace root.
- `recursive` (boolean, optional, default: true): Whether to search recursively into subdirectories.
- `include_gitignored` (boolean, optional, default: false): Whether to also search files matching `.gitignore` patterns.
- `max_results` (number, optional, default: 10): Maximum results per search source.

## Query Modes

The `query` parameter supports three modes, checked in priority order:

### 1. Delimited Regex — `/pattern/flags`
Starts and ends with `/`, with optional flag letters after the trailing `/`.
- Example: `/func.*Handler/g`, `/error/im`
- The pattern between `/` is compiled as-is into a Go regular expression.
- Supported flags: `i` (case-insensitive), `m` (multiline), `s` (dot-all). `g` is ignored (Go MatchString is always global).
- Results weighted: 80% grep, 20% context.

### 2. Wildcard Pattern — contains `*`, `?`, or `[...]`
When the query contains explicit glob wildcards but is NOT a delimited regex.
- Example: `func*Handler`, `[a-z]*test`, `func?test`
- `*` matches any sequence of characters, `?` matches a single character, `[...]` is a character class.
- Other regex metacharacters (`. + ^ $ | ( )`) are treated as literals.
- Results weighted: 80% grep, 20% context.

### 3. Plain Text — default
No special wildcards or delimiters. Simple substring matching.
- Example: `hello world`, `func Handler`
- AI Grep uses `strings.Contains` for content matching.
- Context Engine uses BM25 + vector hybrid retrieval (mode: auto).
- No weighting; both grep and context use full `max_results`.

## Behavior

When `online=false`, performs two-phase local search:
1. **AI Grep**: Search file contents respecting `.alkaid0`, `.gitignore`, and sensitive path exclusions.
2. **Context Engine**: Search indexed codebase with BM25 + vector hybrid retrieval.

Results are merged with source markers (`grep` / `context`).

When `online=true`, searches the internet via configured search engines (Bing, GitHub, arXiv, Tavily) and summarizes the results via LLM before returning.
