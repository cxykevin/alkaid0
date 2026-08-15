### Tool: `fetch`

Make an HTTP request to a remote URL and save the response in a temporary read-only object. Use `read` to inspect the returned `path`. Use `read` for local files and `search` for codebase searches; never use this tool with `file://` or local paths.

#### Parameters

- `method` (string, required): HTTP method, case-insensitive. Supported values include `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, and `HEAD`.
- `url` (string, required): A remote HTTP(S) URL.
- `headers` (string, optional): One `Key: Value` header per line. Explicit headers override tool defaults. Do not put secrets in a prompt or expose them in output unless the request is authorized.
- `body` (string, optional): Raw request body, usually for `POST`, `PUT`, or `PATCH`. A body on `GET` is allowed by the implementation but is unusual.
- `hidden` (boolean, optional, default `false`): Use Chrome-compatible TLS and browser-style headers. This changes request fingerprinting; use only when the target and purpose authorize it. It does not bypass authentication, authorization, or access controls.
- `summary` (string, optional): If non-empty and a summary model is configured, replace the stored response body with an LLM-generated Markdown summary following this instruction. Prefer it for large HTML pages when raw markup is not needed; omit it when exact API/debug output is required.
- `timeout` (number, optional, default `30`): Request timeout in seconds. Values above 30 are capped at 30; zero or negative values use 30.

#### Return value

- `success`: `true` when an HTTP response was received, including 4xx/5xx; `false` for request, DNS, TLS, timeout, body-read, or storage errors.
- `path`: The read-only `@temp/fetch/...` object containing request metadata, status, and raw content or the summary. Use `read` on this path to inspect it.
- `status_code`: HTTP status code when a response was received.
- `truncated`: Present as `true` when the raw body exceeded 64 KiB and was truncated before optional summarization.
- `error`: Present when `success` is `false`.

An HTTP error status is not a transport failure: inspect `status_code` and the body before deciding what to do next. A summary failure falls back to the raw response when possible.

#### Authorization and examples

GET is commonly safe to approve, but all non-GET methods can change remote state and require the applicable approval. Do not send credentials, destructive requests, or private data without explicit authorization.

- Summarize a page: `{"method":"GET","url":"https://example.com","summary":"Summarize the main facts with source links"}`
- Call an API: `{"method":"POST","url":"https://api.example.com/data","headers":"Content-Type: application/json","body":"{\"key\":\"value\"}"}`
- Use browser-compatible transport: `{"method":"GET","url":"https://example.com","hidden":true}`
