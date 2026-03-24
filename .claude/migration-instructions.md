# Channel Architecture Migration Instructions

Send this to the admin channel agent in a fresh session.

---

I need you to set up the new channel architecture. Do each step sequentially — wait for each to succeed before moving on.

IMPORTANT: BotFather operations must be done ONE AT A TIME. Never call telegram_client_create_bot in parallel.

**Step 1: Delete the old dynamic assistant channel**

channel_delete name=assistant

The static assistant channel from config will remain.

**Step 2: Create dev-monitor channel**

channel_create:
  name: dev-monitor
  description: Automated dev maintenance — deploy checks, memory review, repo monitoring, changelog tracking
  type: telegram
  allowed_users: [830516211]
  tool_groups: [core_tools, safe_builtins, channel_management, channel_messaging, scheduling, dev_workflow, repo_monitoring]
  creatable_groups: [core_tools, safe_builtins, channel_messaging, dev_workflow, repo_monitoring]
  links:
    - target: admin
      description: Send if deploy needed, PR opened, or repo change needs manual attention

No telegram_config needed — the bot is created automatically server-side.

**Step 3: Create personal-monitor channel**

channel_create:
  name: personal-monitor
  description: Personal life automation — email checking, commute info
  type: telegram
  allowed_users: [830516211]
  tool_groups: [core_tools, safe_builtins, channel_management, channel_messaging, scheduling, gsuite_read, personal_services]
  creatable_groups: [core_tools, safe_builtins, channel_messaging, gsuite_read, gsuite_write, personal_services]
  links:
    - target: assistant
      description: Send actionable items — emails needing reply, calendar conflicts, commute disruptions

**Step 4: Migrate admin schedules to dev-monitor**

Use schedule_list to get the exact cron expressions and prompts from the 6 admin schedules. For each: schedule_create with same cron/prompt but channel_name=dev-monitor, then schedule_delete the original.

1. Daily deploy check (6am)
2. Daily memory review (8am)
3. Google Workspace CLI monitor (9am)
4. nanoclaw monitor (10am)
5. openclaw monitor (11am)
6. Claude changelog monitor (2pm)

**Step 5: Migrate assistant schedules to personal-monitor**

Same approach for the 3 assistant schedules:

1. Email check (7am, 12pm, 6pm daily)
2. TfL line status (Tue/Thu 7am)
3. TfL bus times (Tue/Thu 7:20am)

**Step 6: Delete the expired restaurant reminder schedule**

**Step 7: Verify**

Run channel_list and schedule_list. Expected:
- Channels: admin (static), assistant (static), dev-monitor (dynamic), personal-monitor (dynamic)
- Schedules: 9 active across dev-monitor and personal-monitor, 0 on admin and assistant
- No expired restaurant reminder

Report the full output to me.
