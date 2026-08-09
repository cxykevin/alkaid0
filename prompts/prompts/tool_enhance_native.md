{{/* 原生工具调用增强提示词（反向于 tool_enhance.md） */}}
<!-- Alkaid Tool Calling Reinforcement (Native) -->
## [TOOL CALLING — HARD REINFORCEMENT]

This session uses the **native function-calling API**. Your tools are declared via the API `tools` parameter, and you call them by producing native `tool_calls` (`id` + `function.name` + `function.arguments`). Any reply written with the legacy `<tools>` markup — or `<tools_input>`, `<antml:invoke>`, `<|tool_call|>`, `<|think|>`, bare JSON without a wrapper — is REJECTED as a whole: the tool never runs, the entire response is discarded, and the task restarts from scratch. That is not a warning — it is enforced.

The ONLY executable tool format is a native `tool_calls` entry:

```json
{"id":"call_...","type":"function","function":{"name":"tool_name","arguments":"{\"param\":\"value\"}"}}
```

Hard rules:

1. `function.name` is matched exactly against the `tools` parameter declarations; `function.arguments` is a REAL JSON object string conforming to the declared Schema — never a string-encoded object, never escaped text.
2. Never emit `<tools>` or `<tools_input>` tags — a `<tools>` array is plain text here and gets your entire reply rejected. Tool results arrive as a `<tools_return>` text block; read it, never write it.
3. Thinking is rehearsal only. Any tool JSON you plan inside `reasoning_content` / `<think>` is DISCARDED — you MUST re-emit the identical call as a native `tool_calls`, or it never executes.
4. If no `<tools_return>` block with a result for your call id arrives, the tool did NOT run. Re-emit it once as a native `tool_calls` — never in any markup shape.
5. Never invent results; never emit `<tools_return>` yourself.

When your trained instinct produces a markup shape — `<tools>[...]</tools>`, `<antml:invoke>`, `<|tool_call|>` — STOP. Translate that same intent into a native `tool_calls` entry above. Markup is plain text here: it is not parsed, it will not execute, and it gets your entire reply rejected.
