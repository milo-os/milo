---
name: github-sub-issues-via-graphql
description: How to create/link/list GitHub sub-issues (parent-child) via gh GraphQL — no native gh command
metadata:
  type: reference
---

GitHub sub-issues (parent→child hierarchy) have no native `gh issue` command;
use the GraphQL API with the `GraphQL-Features: sub_issues` header.

Get a node id:
```
gh api graphql -f query='query{repository(owner:"OWNER",name:"REPO"){issue(number:NUM){id}}}' --jq '.data.repository.issue.id'
```

Link child under parent:
```
gh api graphql -H "GraphQL-Features: sub_issues" \
  -f query='mutation($p:ID!,$c:ID!){addSubIssue(input:{issueId:$p,subIssueId:$c}){subIssue{number}}}' \
  -f p="$parent_id" -f c="$child_id"
```

List a parent's sub-issues:
```
gh api graphql -H "GraphQL-Features: sub_issues" \
  -f query='query{repository(owner:"OWNER",name:"REPO"){issue(number:NUM){subIssues(first:20){nodes{number title state}}}}}' \
  --jq '.data.repository.issue.subIssues.nodes[] | "#\(.number) \(.title)"'
```

Gotchas: `gh` must run inside the repo directory (it shells out to git for the
default repo — fails with "not a git repository" from `/tmp`). Capturing a
`gh issue create` URL via `$(... | tail -1)` silently yields empty if gh errored
to stderr; confirm with `gh issue list`.
