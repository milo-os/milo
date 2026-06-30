---
name: unattended-loop-permission-discipline
description: To run a /loop hands-off with zero permission prompts, use the Write tool for files and one allowlisted Bash pattern per call — no heredoc/compounds
metadata:
  type: feedback
---

To run a recurring `/loop` fully unattended (no permission prompts for hours),
every tool call must match an allowlisted permission.

**Why:** ad-hoc Bash is what breaks unattended runs. `cat > f <<EOF` heredocs,
`a && b` compounds, and jq pipes do not match exact allowlist entries → each one
prompts and stalls the loop.

**How to apply:**
- Write files (issue bodies, comments, manifests) with the **Write tool**, never
  `cat`/heredoc. With `defaultMode: acceptEdits` in `.claude/settings.json`,
  Write/Edit auto-approve.
- Keep every Bash call to ONE allowlisted glob pattern; no `&&` compounds (each
  segment must independently match an allow entry).
- Pre-add broad patterns to `.claude/settings.local.json` allow list, e.g.
  `Bash(jq *)`, `Bash(date *)`, `Bash(gh issue *)`, `Bash(gh pr *)`,
  `Bash(git *)`. MCP tools must each be allowlisted by full name.
- Pace with `ScheduleWakeup` (~1800s when idle); comment on the tracking issue
  ONLY when state changes, else stay silent to avoid issue spam.
