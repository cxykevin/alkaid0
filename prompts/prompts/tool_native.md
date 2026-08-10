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
5. Every key in `function.arguments` must come from the function's declared `properties`, and every value must be a real value you actually have — never a schema field, a placeholder, or an empty stand-in.
6. A result containing `"success": false` or an `"error"` field means the call failed. Fix the arguments (wrong key, wrong type, or missing value) before retrying — never repeat the same call verbatim.

## 3. Filling `function.arguments` correctly (the #1 source of tool failures)

Each function declared in the `tools` parameter lists its parameters under `properties`, with an exact `name` and a `type` (plus which ones are `required`). Your `function.arguments` supplies VALUES for exactly those declared names.

- **Match `properties` exactly.** Use the declared key — `"location"`, not `"city"`, not `"Location"`, not `"loc"`. A key absent from `properties` is an unknown parameter and the call fails.
- **Values only, never the schema.** `{"location":{"type":"string","description":"..."}}` is the schema echoed back as values — wrong. Write `{"location":"Beijing"}`.
- **Values must be real facts you actually have** — from the user message, an earlier tool result, or the conversation context. Never invent or guess a value.
- **Never send placeholders or empty stand-ins.** `{}`, `null`, `"..."`, `"TODO"`, `"unknown"` are not values. If you lack a value, omit the optional parameter (or ask the user) instead of fabricating one.
- **Match the declared `type`.** `"count": 3` is a JSON number and `"enabled": true` is a JSON boolean — never `"3"` or `"true"`.
- **Cover every `required` parameter** listed in the schema's `required` array.

Before emitting a call, re-read the function's `properties` and check your `arguments` key for key, type for type, value for value.

## 4. Replayed tool calls in history

When your earlier assistant turns are replayed, the `arguments` of calls that **succeeded** are abbreviated to `"..."` — the runtime shortens them to save tokens; that is normal, and you do not need to reconstruct them. The `arguments` of calls that **failed** are shown in full, exactly as you passed them, so you can see what went wrong. A result carrying `"success": false` or an `"error"` field marks a failed call: identify the wrong key, type, or missing value in those arguments and correct it before retrying — do not repeat the same call unchanged.

## 5. Example

Tools declared via the API: `get_weather(location: string)`.

User: "Please get the weather for Beijing and Shanghai."

You reply with native `tool_calls`:

```json
{"id":"call_ab12cd","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Beijing\"}"}}
{"id":"call_ef34gh","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}}
```

The runtime executes both and delivers each result back as a `role:"tool"` message matched by `tool_call_id` (`call_ab12cd`, `call_ef34gh`). Continue the conversation using those results.
