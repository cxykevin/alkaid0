### Tool: `fetch`

#### Description:

Make HTTP requests to arbitrary URLs and return the response. Useful for calling REST/debug APIs, fetching web pages, and checking endpoints. The response is written to a temp file (`@temp/fetch/...`) whose content is returned via the `path` field — the file head shows the original request (method, URL, status) so you can recognize what it corresponds to.

#### Parameters:

- `method` (string, **required**): HTTP method, use UPPERCASE: `GET` / `POST` / `PUT` / `DELETE` / `PATCH` / `HEAD`.
- `url` (string, **required**): The URL to request, e.g. `https://example.com/api`.
- `headers` (string, optional): Request headers, one `Key: Value` per line. Example:
  ```
  Content-Type: application/json
  Authorization: Bearer xxx
  ```
- `body` (string, optional): Raw request body string (mainly for `POST`/`PUT`). Not recommended for `GET`.
- `hidden` (boolean, optional, default `false`): If `true`, disguise the request as a real Chrome browser: utls Chrome TLS fingerprint + browser-style headers (User-Agent, Sec-Fetch-\*, Referer, DNT). Use this when a target site blocks non-browser clients. `false` uses the built-in Alkaid0 user agent.
- `summary` (string, optional): If non-empty, the fetched content is summarized into markdown via LLM using this text as the summary instruction, and the markdown summary is written to the temp file instead of the raw body. **When fetching a web page (HTML content) — not a debug/JSON API — you SHOULD always set this parameter** so the response stays concise and readable.
- `timeout` (number, optional, default `30`): Timeout in seconds. Values above `30` are truncated to `30`.

#### Return:

Returns a JSON object:
- `success` (boolean): `true` when the HTTP round-trip completed (even for 4xx/5xx). `false` only on transport errors (timeout / DNS / TLS / network), in which case `error` contains the message.
- `path` (string): The `@temp/fetch/...` path of the response temp file. Read this path to get the content. The file head includes the request info (`[fetch] METHOD url`, `Status: code`, request headers/body if any) followed by the response body (or the LLM markdown summary when `summary` is set).
- `status_code` (number): HTTP status code of the response (e.g. `200`, `404`, `403`).
- `truncated` (boolean, optional): present and `true` when the raw body exceeded the 64KB limit and was truncated.
- On transport errors only: `success` is `false` and `error` contains the message.

#### Behavior:

- `method` is case-insensitive (sent uppercase internally). Use uppercase to match the auto-approval rule (`GET` is auto-approved, other methods require manual approval).
- User-supplied `headers` always override the defaults set by the tool.
- For `hidden=true`, a Chrome TLS fingerprint is used even when the global `Agent.FetchProxy` proxy is configured (manual CONNECT/socks5 tunnel + utls).
- GET with a body is allowed but unusual; prefer POST/PUT when sending a body.

#### Quick Examples:

- Fetch a web page: `{"method":"GET","url":"https://example.com","summary":"Summarize the key points of this page","hidden":true}`
- Call a JSON API: `{"method":"GET","url":"https://api.example.com/v1/status","headers":"Authorization: Bearer token","timeout":10}`
- POST JSON: `{"method":"POST","url":"https://api.example.com/v1/items","headers":"Content-Type: application/json","body":"{\"name\":\"test\"}"}`
