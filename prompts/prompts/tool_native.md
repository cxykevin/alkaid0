{{/* Native tool calling prompt (counterpart of tools.md) */}}
# Tool Calling Protocol (Native)

## 1. How to call a tool

This session uses the **native function-calling API**. Your tools are declared through the API `tools` parameter, and you call them by emitting native `tool_calls` entries. Each call has three parts:

- `id` — a unique id **you generate** for this call, in the conventional `call_` form (e.g. `call_ab12cd`). Every call needs its own id; the runtime uses it to match the result back to you.
- `function.name` — must exactly match one of the tool names declared in the `tools` parameter. Inventing a name makes the call fail.
- `function.arguments` — a JSON **object string** carrying the parameter values, e.g. `"arguments":"{\"location\":\"Beijing\"}"`. Values only — never the schema, never a string-encoded object, never escaped text.

You may batch several `tool_calls` entries in one reply.

## 2. Rules

1. `function.name` must match a declared tool exactly.
2. `function.arguments` is a JSON object string, never an object literal or a nested JSON-encoded string.
3. Thinking (`reasoning_content`) is rehearsal only — any call you plan there must be re-emitted as a real `tool_calls` entry, or it never executes.
4. A tool call runs only after its result arrives as a `role:"tool"` message carrying your `id` (the `tool_call_id`). Until that result arrives, the tool did not run — re-emit that same call once.

## 3. Example

Tools declared via the API: `get_weather(location: string)`.

User: "Please get the weather for Beijing and Shanghai."

You reply with native `tool_calls`:

```json
{"id":"call_ab12cd","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Beijing\"}"}}
{"id":"call_ef34gh","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}}
```

The runtime executes both and delivers each result back as a `role:"tool"` message matched by `tool_call_id` (`call_ab12cd`, `call_ef34gh`). Continue the conversation using those results.
