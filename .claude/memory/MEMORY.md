# Shared operational memory

Version-controlled operational notes for working on this repo with Claude Code
(or any agent). Each entry below points to one file holding one fact. These are
the *public, repo-portable* subset of operating knowledge — tooling habits, git
hygiene, unattended-run discipline, and milo-specific architecture that is not
already obvious from the code or CLAUDE.md.

Keep entries here generic and non-sensitive: no credentials, cluster names,
project IDs, or internal-only incident detail.

- [gh issue/PR body via --body-file](gh-issue-body-file.md) — use `--body-file` not `--body "$(cat <<EOF)"` so code fences render
- [GitHub sub-issues via gh GraphQL](github-sub-issues-via-graphql.md) — no native `gh` command; use the `addSubIssue` mutation with the `sub_issues` feature header
- [Pull main on switch](pull-main-on-switch.md) — every checkout of main: `git fetch` + `git pull --ff-only` before acting
- [Unattended-loop permission discipline](unattended-loop-permission-discipline.md) — for a zero-prompt `/loop`: Write tool for files, one allowlisted Bash pattern per call, no heredoc/compounds
- [Activity policies owned by source repos](activity-policies-owned-by-source-repos.md) — milo owns its own ActivityPolicy CRs under `config/services/activity/policies/`, shipped to the control plane via an OCI bundle
