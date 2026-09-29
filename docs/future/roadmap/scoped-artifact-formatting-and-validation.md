---
title: scoped artifact formatting and validation
state: inbox
created: 2026-09-29
tags: [enhancement]
---

provide an MCP equivalent of `unfurl -i` so an agent can reflow the markdown it edits without needing general shell access. prefer a narrow path-based tool wrapping the existing executable, preserving the same formatting behavior coding agents use. report completion, whether the file changed, and failures. use the tool server's file-access boundaries and treat formatting as a write for approval purposes. choose an existing filesystem MCP server or a small companion server as its home; pane should consume it through its existing MCP integration, not acquire another execution path.

## why

file-writing access alone does not let an agent complete the required markdown-formatting step. exposing `unfurl` closes that gap without adding a shell runner or a general validator framework. capturing this card does not authorize changing the running MCP configuration.
