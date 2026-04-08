---
name: update
description: Pull latest tclaw, rebuild, and optionally redeploy
disable-model-invocation: true
allowed-tools: Bash, Read
---

Update tclaw to the latest version. Run steps in order, stop on failure.

## 1. Check for uncommitted changes

```bash
git status --porcelain
```

If there are uncommitted changes, warn the user and ask whether to continue (changes will be preserved — we're pulling, not resetting).

## 2. Pull latest

```bash
git pull --rebase origin main
```

If this fails due to conflicts, stop and explain. Do not force-pull or reset.

## 3. Rebuild

Check how tclaw was installed and rebuild accordingly:

```bash
# Check if using dev mode (shell function) or compiled binary
grep -q "tclaw dev (do not edit)" ~/.zshrc 2>/dev/null && echo "dev-mode" || echo "compiled"
```

- **Dev mode:** No rebuild needed — shell function runs from source
- **Compiled:** Run `make install` to rebuild binaries

## 4. Restart local server (if running)

```bash
pgrep -f "tclaw.*serve" > /dev/null 2>&1 && echo "Server is running — needs restart" || echo "Server not running"
```

If running, tell the user to restart: stop the current server (Ctrl+C) and run `tclaw serve` again. Do NOT kill the process — let the user do it.

## 5. Redeploy (if applicable)

Only if `fly.toml` exists:

```bash
test -f fly.toml && echo "Fly deployment found" || echo "No deployment configured"
```

If deployed, ask the user if they want to redeploy. If yes:

```bash
# Check if CI will handle it (push to main triggers deploy)
git log --oneline origin/main..HEAD | wc -l | xargs echo "Unpushed commits:"
```

- If there are unpushed commits and CI is configured: pushing will trigger automatic deploy
- If they want manual deploy: `go run . deploy`
- Remind them to push secrets if config changed: `go run . deploy secrets`

## 6. Summary

Report what was updated:
- Previous commit → new commit
- Whether rebuild was needed
- Whether redeploy is needed/pending
