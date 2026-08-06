---
name: pull-main-on-switch
description: Every time you switch to the main branch, git fetch then pull --ff-only before acting
metadata:
  type: feedback
---

Every time you check out / switch to `main`, immediately `git fetch origin`
then `git pull --ff-only origin main` before doing anything else on main.

**Why:** local main goes stale fast — image-update automation and merged PRs
land on origin/main constantly, and acting on a stale main causes confusion
(e.g. thinking a merged change is not present, or branching from an old base).

**How to apply:** treat the fetch + ff-only pull as one step that always follows
`git checkout main`, before branching or inspecting state.
