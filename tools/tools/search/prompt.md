Search tool — search the codebase using AI rules (grep) and context engine (BM25 + vector).

## Parameters
- `query` (string, required): The search query.
- `online` (boolean, required): Whether to search online. Currently only `false` is supported.
- `include_gitignored` (boolean, optional, default: false): Whether to also search files matching `.gitignore` patterns.
- `max_results` (number, optional, default: 10): Maximum results per search source.
- `context_search_type` (string, optional, default: "auto"): Context engine search mode: "auto" (BM25+vector hybrid), "bm25" (keyword only), "vector" (semantic only).

## Behavior
When `online=false`, performs two-phase local search:
1. **AI Grep**: Search file contents respecting `.alkaid0`, `.gitignore`, and sensitive path exclusions.
2. **Context Engine**: Search indexed codebase with BM25 + vector hybrid retrieval.

Results are merged with source markers (`grep` / `context`).
