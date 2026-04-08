---
name: status
description: Quick overview of local server, deployment, and config state
disable-model-invocation: true
allowed-tools: Bash, Read, Grep
---

Print a concise status overview. Check each area and report in a compact format.

## Checks

Run all of these and print results:

### Local
```bash
# Server running?
pgrep -f "tclaw.*serve" > /dev/null 2>&1 && echo "Server: running (PID $(pgrep -f 'tclaw.*serve'))" || echo "Server: not running"

# Socket files (active channels)
ls /tmp/tclaw/*/*.sock 2>/dev/null | while read f; do basename "$f" .sock; done | paste -sd, - || echo "Channels: none"
```

### Git
```bash
# Current commit
echo "Local: $(git log --oneline -1)"

# Uncommitted changes
git status --porcelain | head -5
```

### Deployment
Only if `fly.toml` exists:
```bash
if [ -f fly.toml ]; then
    APP=$(grep "^app" fly.toml | cut -d'"' -f2)
    echo "Fly app: $APP"
    
    # Deployed commit (from fly status or image tag)
    fly status -a "$APP" 2>/dev/null | head -5
    
    # Config drift
    go run . config diff 2>/dev/null | head -10 || echo "Config diff: unable to check"
else
    echo "Deployment: not configured (no fly.toml)"
fi
```

### Config
```bash
# Environment sections
grep "^[a-z_]*:" tclaw.yaml 2>/dev/null | sed 's/://'

# Channel count
grep "type:" tclaw.yaml 2>/dev/null | wc -l | xargs echo "Channels configured:"

# Users
grep "id:" tclaw.yaml 2>/dev/null | head -5
```

## Output format

Keep it compact — one line per item, grouped by section:

```
=== Local ===
Server: running (PID 12345)
Channels: main, mobile

=== Git ===
HEAD: abc1234 docs: rewrite README
Clean: yes

=== Deployment ===
App: my-tclaw
Status: running
Config drift: none

=== Config ===
Environments: local, prod
Channels: 2 configured
Users: default
```
