---
name: cpd
description: Commit, push, and deploy
disable-model-invocation: true
allowed-tools: Bash
---

Run the full commit-push-deploy pipeline for tclaw:

1. Run `git status` and `git diff --stat` to see what's changed
2. Stage all modified and untracked files (but NOT files that look like they contain secrets — .env, credentials, etc.)
3. Write a concise commit message summarizing the changes
4. Commit with `Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>`
5. Push to remote with `git push`
6. Deploy with `go run . deploy` (this builds locally and deploys to Fly.io)

If there are no changes to commit, skip straight to deploy (in case there are already-pushed commits that haven't been deployed yet).

If the deploy command asks for confirmation or has a non-zero exit, report the output and stop.
