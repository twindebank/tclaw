---
name: channel-rules
description: "How the rulebooks in ./rules/ work — where a standing decision goes, the shape of an entry, which channels load which rulebook, and how to get one changed. Read before writing down a rule the user has given you, before proposing a change to one, and when deciding whether a rule already covers what you are about to do."
metadata:
  category: "memory"
---

# Rulebooks

`./rules/` holds the user's standing decisions about how work gets done, one file per area. A rulebook
is what you follow instead of deciding again. Memory files are different: they hold what you learned,
and you write those freely.

## Is this a rule?

| It is | It is not |
|---|---|
| "Never send an invoice before the work is signed off" | "The invoice went out on Tuesday" |
| "Use the shed sensor, not the outdoor one, for frost" | "The shed sensor reads 3° right now" |
| Decided once and applied every time | True today and stale next week |

A fact goes in memory or the vault. A preference the user restates every time is a rule — write it down
so they stop having to.

## Where it goes

One file per area of work, named for the area: `automations.md`, `invoices.md`, `running.md`. A rule
that only ever applies on one channel still lives in the shared pool; the channel decides what loads,
not what exists.

Put a rule in an existing rulebook where one fits. A new file is for a genuinely new area — three
rulebooks with two rules each are harder to find things in than one with six.

## The shape of an entry

```
## Never send an invoice before the work is signed off
Applies to: the email channel, any outgoing payment request

Wait for the sign-off message, then send. If it has been more than a week, ask rather than assuming.

- why: "we got burned on this twice, I'd rather chase than write it off" (2026-08-14)
- check: a draft invoice with no sign-off anywhere in the thread
```

`why:` records what the user actually said and when. It is what stops the rule being dropped the first
time it is inconvenient. `check:` describes what a breach looks like, concretely enough to spot.

## Which channels load it

Each channel's `./channels/<name>/CLAUDE.md` has two lists:

- **Loaded** — an `@../../rules/<file>.md` line per rulebook. These arrive in context on every turn of
  that channel. Use this for rulebooks that apply to most of the work there.
- **Available** — a line per rulebook that applies to occasional work here, saying what should send you
  to it. These are read on demand.

Everything else is still readable. `rule_list` shows every rulebook and where each one is referenced.
Nothing is hidden from a channel; loading only decides what you get without asking.

Editing a channel's CLAUDE.md is ordinary memory work — no approval needed. Moving a rulebook between
the two lists is exactly that kind of edit.

## Changing a rule

Rulebooks are the user's decisions, so you propose and they decide.

1. Read the current file, if it exists.
2. Call `rule_propose` with the file name, the **complete** text it should have, and why. Not a patch —
   the user approves exactly what gets written, so include the parts you are keeping.
3. Stop. They get the prompt directly and their reply saves it. Do not answer for them, and do not send
   a follow-up message about it.

Write and Edit to `./rules/` are refused, so this is the only route. That refusal is the point: a rule
you can quietly rewrite is not a rule.

## When a rule and an instruction disagree

Say so rather than picking one silently. "There's a rule that says X — do you want me to do Y this time,
or change the rule?" A one-off exception is fine and needs no rule change; a second one means the rule
is wrong and should be proposed as an amendment.
