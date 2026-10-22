### Tool: `time`

Return the current local time of the machine running alkaid0, including the UTC offset.

#### Parameters

This tool takes no parameters. Call it with an empty object: `{}`.

#### Result

```json
{"success": true, "time": "2026-10-22 22:14:56 GMT+8"}
```

`time` is formatted as `YYYY-MM-DD HH:MM:SS GMT±H[:MM]` in the server's local timezone.
Whole-hour zones omit the minutes (`GMT+8`, `GMT+0`, `GMT-4`); half/quarter-hour zones
include them (`GMT+5:30`, `GMT-3:30`).

#### When to use

- The system prompt only injects the current **date** (`Today date is: ...`). Use this tool
  when the exact time of day matters: writing timestamps, telling the user what time it is,
  or computing how long until a scheduled moment.
- Times are in the user's local timezone, so `HH:MM:SS` can be taken as-is without further
  conversion.
