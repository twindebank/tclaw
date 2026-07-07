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

## Saving changes

Just edit files. tclaw auto-syncs the vault in the background after every turn — committing
any changes, rebasing onto the remote, and pushing — so you never need to run git yourself.

If a rebase hits a conflict, the background sync backs out cleanly (your commit stays intact,
just unpushed) and sends you a one-off notification rather than surfacing it as a message you
need to act on mid-turn. When you see that notification, resolve it with raw git:

```
cd {{path}}
git pull --rebase   # reproduces the conflict
# ...resolve conflict markers...
git add -A
git rebase --continue
git push
```
