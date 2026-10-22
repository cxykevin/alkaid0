### Tool: `cron`

Create, modify or delete scheduled prompts that are delivered **back into the current
session** as ordinary user messages (prefixed with `[cronjob] `). The session is woken up
when a job fires, so the prompt runs as a normal agent turn.

Jobs are owned by the current workdir **and** the current session id; you can only see and
modify jobs of this session. The list of this session's jobs is also injected as the
read-only virtual object `@cron`:

```
- 3(0 9 * * *) Summarize yesterday's commits
- 4(30 17 * * 5 once) Remind me to prepare the release notes
```

The number right before `(` is the job id — that is the `id` to pass to `edit` and
`delete`; a trailing `once` marks a one-shot job.

#### Parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `type` | string | yes | `add`, `edit` or `delete` |
| `id` | number | for `edit`/`delete` | Job id. Must be omitted for `add` |
| `cron` | string | for `add` | Schedule expression (5 fields or `@daily`-style shorthand, local timezone) |
| `prompt` | string | for `add` | Text delivered at fire time, fewer than 200 characters |
| `run_once` | boolean | no | `true` = delete after the first successful delivery (default for `add`), `false` = repeat |

`cron` uses the standard 5-field syntax `minute hour day-of-month month day-of-week`
and the predefined descriptors `@yearly`, `@monthly`, `@weekly`, `@daily`, `@hourly`.
`@every <duration>` is **not** supported, and neither is a seconds field (6 fields).
All times are interpreted in the server's local timezone.

Examples: `0 9 * * *` (every day 09:00), `*/30 * * * *` (every 30 minutes),
`0 18 * * 1-5` (weekdays 18:00), `0 0 1 * *` (first day of every month 00:00).

Shorthand is accepted and stored **already expanded**, so results, `@cron` lines and
`next_runs` always show the 5-field form:

| shorthand | stored as |
|---|---|
| `@hourly` | `0 * * * *` |
| `@daily` / `@midnight` | `0 0 * * *` |
| `@weekly` | `0 0 * * 0` |
| `@monthly` | `0 0 1 * *` |
| `@yearly` / `@annually` | `0 0 1 1 *` |

`prompt` is trimmed; it must be non-empty and **fewer than 200 characters** counted in
Unicode characters (the `[cronjob] ` prefix added on delivery does not count). Write a
self-contained instruction: when the job fires there is no extra context.

#### Operations

- `add` — requires `cron` and `prompt`. `run_once` defaults to `true`; pass
  `run_once: false` only when the task really repeats. A session may have at most **4**
  repeating jobs (`run_once: false`); one-shot jobs are not limited. `id` must not be sent.
- `edit` — requires `id`; pass `cron` and/or `prompt`, only the fields you pass are
  changed. `run_once` cannot be changed: delete the job and add a new one instead.
- `delete` — requires `id`; takes no other field (`cron`/`prompt` are ignored,
  `run_once` is rejected).

#### Result

```json
{"success": true, "job": {"id": 3, "cron": "0 9 * * *", "run_once": true,
  "prompt": "Summarize yesterday's commits", "workdir": "/path/to/project",
  "session_id": 12, "next_runs": ["2026-10-23 09:00:00 GMT+8"]}}
```

`next_runs` lists the next 4 fire times (only the next 1 for `run_once: true`), formatted
like the `time` tool. It is present for `add` and for an `edit` that changes `cron`; a
`prompt`-only edit omits it.

`delete` returns `{"success": true, "id": 3}`. Any validation failure returns
`{"success": false, "error": "..."}` with the concrete reason — fix the parameters and
call again. Do not retry the identical call.

#### Notes

- Jobs survive restarts. A job is dropped when its workdir or session no longer exists,
  its expression becomes invalid, or its prompt is empty.
- Missed fire times while the server was down are **not** replayed; the next occurrence is
  computed from the current time.
- If the session is busy with another turn when the job fires, that occurrence is skipped
  (it is not queued) — but a one-shot job is only consumed after a successful delivery.
- Times reported by this tool are the schedule's local times; convert times requested in
  another timezone yourself if needed.
