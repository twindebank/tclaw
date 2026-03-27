---
name: clean-commits
description: Rewrite git history to normalize emails, names, and timestamps across all commits
disable-model-invocation: true
allowed-tools: Bash, Read
---

Rewrite all commits to normalize author/committer identity and assign sequential timestamps. This is a destructive history rewrite — all commit hashes will change.

## Target State

| Field | Non-tclaw commits | tclaw commits |
|-------|-------------------|---------------|
| Author name | `Theo Windebank` | `tclaw` (keep) |
| Author email | `twindebank@users.noreply.github.com` | `tclaw@localhost` (keep) |
| Committer name | `Theo Windebank` | `tclaw` (keep) |
| Committer email | `twindebank@users.noreply.github.com` | `tclaw@localhost` (keep) |
| Timestamps | Sequential per day: `YYYY-MM-DD 00:01:00 +0000`, `00:02:00`, etc. | Same sequential scheme |

Timestamps use each commit's **original date** but replace the time with sequential minutes within that day, in commit order.

## 1. Check prerequisites

```bash
git-filter-repo --version
```

If missing: `brew install git-filter-repo` or `pip install git-filter-repo`. Stop until installed.

## 2. Audit current state

Show what needs cleaning:

```bash
echo "=== Emails ==="
git log --format="%ae %ce" | sort | uniq -c | sort -rn

echo "=== Names ==="
git log --format="%an / %cn" | sort | uniq -c | sort -rn

echo "=== Uncleaned timestamps (not matching 00:XX:00 pattern) ==="
git log --format="%ai %s" | grep -cv "00:0[0-9]:00 +0000"
echo "commits need timestamp cleanup"

echo "=== Total commits ==="
git rev-list --count HEAD
```

Show the user and confirm they want to proceed before continuing.

## 3. Run the rewrite

Write a temporary Python callback script and run it:

```bash
cat > /tmp/clean-commits.py << 'PYEOF'
import datetime

# Track per-day commit counts for sequential timestamps.
# filter-repo processes commits oldest-first, so counters increment naturally.
day_counters = {}

def clean_commit(commit, _metadata):
    author_email = commit.author_email.decode()
    is_tclaw = (author_email == "tclaw@localhost")

    # Normalize identity (skip tclaw commits).
    if not is_tclaw:
        commit.author_name = b"Theo Windebank"
        commit.author_email = b"twindebank@users.noreply.github.com"
        commit.committer_name = b"Theo Windebank"
        commit.committer_email = b"twindebank@users.noreply.github.com"

    # Normalize timestamps — sequential minutes per day.
    # Parse the original date (just the date portion).
    original = commit.author_date.decode()
    # Format: "EPOCH TZOFFSET" — convert epoch to date.
    parts = original.split()
    epoch = int(parts[0])
    dt = datetime.datetime.utcfromtimestamp(epoch)
    day_key = dt.strftime("%Y-%m-%d")

    day_counters[day_key] = day_counters.get(day_key, 0) + 1
    minute = day_counters[day_key]

    # Build new timestamp: same date, 00:MM:00 UTC.
    new_dt = datetime.datetime.strptime(day_key, "%Y-%m-%d").replace(
        hour=0, minute=minute, second=0
    )
    new_epoch = int(new_dt.replace(tzinfo=datetime.timezone.utc).timestamp())
    new_date = f"{new_epoch} +0000".encode()

    commit.author_date = new_date
    commit.committer_date = new_date
PYEOF

git filter-repo --force --commit-callback "$(cat /tmp/clean-commits.py)"
```

## 4. Verify

Run all of these and show the output:

```bash
echo "=== Emails (should be exactly 2: twindebank noreply + tclaw) ==="
git log --format="%ae %ce" | sort | uniq -c | sort -rn

echo "=== Names (should be exactly 2: Theo Windebank + tclaw) ==="
git log --format="%an / %cn" | sort | uniq -c | sort -rn

echo "=== Uncleaned timestamps (should be 0) ==="
git log --format="%ai" | grep -cv "00:0[0-9]:00 +0000"

echo "=== Total commits (should match pre-rewrite count) ==="
git rev-list --count HEAD

echo "=== Sample tclaw commits (should show tclaw identity) ==="
git log --format="%ae %an: %s" --all | grep tclaw | head -5

echo "=== First 5 commits ==="
git log --format="%ai %ae %s" --reverse | head -5

echo "=== Last 5 commits ==="
git log --format="%ai %ae %s" | head -5
```

If anything looks wrong, tell the user to `git reflog` and `git reset --hard` to recover.

## 5. Force push

**Ask the user for explicit confirmation before running this.**

```bash
git push --force origin main
```

## 6. Cleanup

```bash
rm -f /tmp/clean-commits.py
```

Remind the user that anyone else with a local clone will need to re-clone or `git fetch --all && git reset --hard origin/main`.
