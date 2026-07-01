---
name: knowledge
description: Read from and write to the personal knowledge base — the durable notes vault. Use whenever recording or recalling durable facts, notes, references, or project knowledge.
---

# Personal knowledge base

Your durable knowledge lives in a git-backed markdown vault checked out at `{{path}}`
(reachable as `../knowledge` from your memory directory). It is your source of truth for
durable facts, notes, references, and project knowledge.

Keep the distinction clear:

- **Operational / behavioural memory** — how to behave, per-channel context, short-lived
  working notes — stays in `memory/CLAUDE.md` and `memory/channels/*/CLAUDE.md`.
- **Durable knowledge** — anything worth keeping about the user's life, projects, and world —
  belongs in the vault, not in memory files.

## Reading

- Start from `{{path}}/index.md` (the vault map), then the relevant domain's `index.md`.
- Load only the notes you actually need. Never read the whole vault into context.

## Conventions

Follow the vault's own `{{path}}/AGENTS.md` exactly — it is the source of truth for structure
and formatting. In short:

- kebab-case filenames, one concept per file, filed in the right folder.
- `type:` frontmatter on every note; bump the `updated:` date on every edit.
- Inline `[[wikilinks]]` in prose; add each new note to its folder's `index.md` map.
- Never write secret values. Reference secrets as `op://<vault>/<item>/<field>` pointers only.

## Saving changes (raw git)

The vault is an ordinary git clone with a preconfigured, authenticated remote — you never
need credentials. To publish changes:

```
cd {{path}}
git pull --rebase          # get the latest before editing
# ...create or edit notes...
git add -A
git commit -m "<clear, specific message>"
git push
```

If `git pull --rebase` reports a conflict, stop and surface it to the user — do not force.
