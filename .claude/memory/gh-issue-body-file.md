---
name: gh-issue-body-file
description: Use --body-file not a --body heredoc for gh issue/PR create/edit to avoid backtick mangling
metadata:
  type: feedback
---

Use `--body-file <path>` instead of `--body "$(cat <<'EOF'...)"` when creating
or editing GitHub issues or PRs whose body contains markdown code fences.

**Why:** Heredoc backtick escaping produces literal backslashes in the rendered
output — fenced code blocks fail to render.

**How to apply:** Write the body to a file with the Write tool (see
[[unattended-loop-permission-discipline]]), then pass `--body-file`. Works for
`gh issue create`, `gh issue edit`, `gh pr create`, and `gh pr edit`.
