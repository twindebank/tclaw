---
name: gws-tclaw
description: "REQUIRED before any Google Workspace / gws operation in tclaw. How to invoke gws through the google_workspace MCP tool (not the shell), which dedicated tools to prefer, and tclaw-specific API gotchas. Read this before using google_workspace, google_gmail_*, or google_calendar_* tools."
metadata:
  category: "productivity"
---

# Google Workspace in tclaw

The `gws-*` skills document the Google Workspace CLI (`gws`). **In tclaw you do NOT run
`gws` in the shell** — there is no token in the sandbox. Ignore any `gws auth login`,
`gws generate-skills`, or bare `gws …` shell instructions in the other skills. Instead,
invoke gws through the **`google_workspace` MCP tool**, which injects your OAuth token
server-side and runs the command for you.

## Translating a gws skill example into a tool call

Every `gws-*` skill shows commands like:

```bash
gws gmail users messages modify --params '{"userId":"me","id":"MSG_ID"}' --json '{"addLabelIds":["LABEL_ID"]}'
```

Map it onto the `google_workspace` tool like this:

| gws CLI piece            | google_workspace argument |
|--------------------------|---------------------------|
| args after `gws`         | `command` (e.g. `"gmail users messages modify"`) |
| `--params <JSON>`        | `params` (the JSON string) |
| `--json <JSON>`          | `json` (the request body JSON string) |
| (which Google account)   | `credential_set` |

So the example above becomes:

```
command = "gmail users messages modify"
params  = {"userId":"me","id":"MSG_ID"}
json    = {"addLabelIds":["LABEL_ID"],"removeLabelIds":["UNREAD"]}
```

Use `google_workspace_schema` (dotted notation, e.g. `gmail.users.messages.modify`) to
inspect a method's parameters when a skill doesn't show them.

## Prefer the dedicated tools

Where a dedicated tool exists, use it instead of `google_workspace` — they're more
efficient and handle edge cases:

- `google_gmail_list` — search/scan email (returns compact summaries, not raw HTML)
- `google_gmail_read` — read a message body. **Never** use `google_workspace` with Gmail
  `format=full`: it returns huge HTML blobs that flood context.
- `google_gmail_send` — compose and send
- `google_gmail_forward` — forward (fetches the full body; handles HTML-only emails,
  unlike `gws gmail +forward` which truncates to the snippet)
- `google_calendar_list` / `google_calendar_create` — list and create events

## tclaw-specific API gotchas

These are quirks observed in production that the generated skills don't cover:

### Calendar
- To edit an event use `calendar events update` (full PUT), **not** `calendar events
  patch` — patching a `date` to a `dateTime` causes a 400.
- For a timezone in `dateTime`, put a UTC offset in the ISO string
  (e.g. `2026-03-13T17:26:00+00:00`), **not** a separate `timeZone` field.

### Sheets
- All write operations (`values.update`, `batchUpdate`, `values.clear`, …) require the
  `json` field — pass the request body there. Example: command=`sheets spreadsheets
  values update`, params=`{"spreadsheetId":"…","range":"Sheet1!A1","valueInputOption":"RAW"}`,
  json=`{"values":[["hello"]]}`.
- **Checkboxes in a Google Sheets Table:** do NOT use `setDataValidation` — it fails with
  "not allowed on cells in typed columns". Use `updateCells` with `fields='dataValidation'`
  instead (works even on typed columns).
- If cells show a validation error after adding checkbox format, they likely hold
  stringValue `"TRUE"`/`"FALSE"` instead of boolValue `true`/`false` — fix with a separate
  `updateCells` request with `fields='userEnteredValue'` and `boolValue: true/false`.
- **Hyperlinks:** set a link with `textFormatRuns` containing a `link` object, NOT a
  `=HYPERLINK()` formula. Pass `userEnteredValue` (stringValue: display text) and
  `textFormatRuns` (`[{startIndex:0, format:{link:{uri:"…"}}}]`) with
  `fields='userEnteredValue,textFormatRuns'`. This matches how Sheets stores "Insert Link".

### Reading PDF attachments from Gmail
1. `google_workspace` with `gmail users messages get`, format=full — result is saved to a
   file (too large for context).
2. Use node to parse the file and find attachment IDs: iterate `payload.parts` recursively,
   look for `body.attachmentId` and `filename`.
3. `google_workspace` with `gmail users messages attachments get`, params
   `{userId:"me", messageId:"…", id:"<attachmentId>"}` — also saved to a file.
4. Use node to base64-decode: `obj.data.replace(/-/g,'+').replace(/_/g,'/')`, then
   `Buffer.from(b64,'base64')`, write to `/tmp/filename.pdf`.
5. Use the Read tool on the saved PDF — Claude can view it directly as multimodal input.
