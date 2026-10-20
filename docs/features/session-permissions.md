---
title: Session permissions
weight: 24
---

Use `/permissions` in a session, or select **permissions** in the command
palette, to review recorded requests and manage Ocman's approval rules.

The dialog includes pending requests, one-time approvals, rejections and
previous automatic approvals. Search the recorded commands or permission
types, then choose **allow**, **ask** or **deny** to add an editable rule.
You can also add rules manually.

For example, after approving `tea do something`, add this rule:

| Permission | Pattern | Action |
| --- | --- | --- |
| `bash` | `tea do *` | `allow` |

Save the rules to apply them to pending and future requests. Rules remain
attached to this session across refreshes and Ocman restarts. They also cover
its nested subagents, but do not carry into separate sessions or worktree
splits.

## Rule behavior

- **Allow** answers matching requests with a one-time approval.
- **Ask** requires your decision, even if Ocman's AI judge or cache would
  otherwise approve the request.
- **Deny** rejects matching requests.
- Without a matching rule, Ocman's existing approval behavior applies.

`*` matches any text and `?` matches one character. Escape a literal wildcard
with a backslash, such as `cat \*`. Rules created from a recorded request
start with escaped wildcards so you can broaden them deliberately. A wildcard
matches command text, not parsed shell subcommands, so keep allow patterns
narrow.

The last matching rule wins for each requested resource. Move rules up or down
to change their priority. If a request contains several resources, any denied
resource rejects the request; all resources must be allowed for automatic
approval. A subagent's own rules take priority over its ancestor's rules.

These rules affect **Ocman's approval logic only**. They do not change the
agent's native configuration, grant access the agent has already refused, or
intercept actions that do not ask Ocman for permission. Remote sessions keep
their rules on their owning machine, which must be connected to view or edit
them.
