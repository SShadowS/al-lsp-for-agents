# Agent-flow tests (proof of concept)

End-to-end tests for the AL LSP wrapper that drive a real Claude Code agent
via the [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview).

These complement, not replace, the Go unit tests:

| Tier | Tool                                | Catches                                                   | Speed | Run on    |
|------|-------------------------------------|-----------------------------------------------------------|-------|-----------|
| 1    | `go test ./wrapper/`                | URI parsing, version compare, handler logic               | ms    | every PR  |
| 2    | `python test_lsp_go.py`             | LSP-protocol-level integration                            | sec   | every PR  |
| 3    | `python agent-flows/find_events.py` | Real agent + real Claude Code tool surface + real wrapper | min   | nightly   |

The friend's failing `documentSymbol` on `al-preview:/...dal` (fixed in v1.11.1)
was the canonical motivating example: tier 1 + 2 were green, the bug only
showed up when a real agent went through Claude Code's filesystem-existence
check on `filePath`. Tier 3 catches that.

## Prerequisites

- `ANTHROPIC_API_KEY` set
- `pip install -r requirements.txt`
- AL LSP plugin enabled in Claude Code:
  ```powershell
  /plugin install al-language-server-go-windows@al-lsp-for-agents
  ```
- `test-al-project/.alpackages/` populated (already committed for this repo)

## Running a scenario

From the repo root:

```powershell
python test-al-project/agent-flows/find_events.py
```

Exit code: `0` if all assertions pass, non-zero on failure. Prints a per-call
trace + per-assertion pass/fail, similar in spirit to the matrix harness.

## Why hook-based capture?

`PostToolUse` hooks see the tool result after execution; iterating
`AssistantMessage.content` only sees Claude's intent (the `ToolUseBlock`).
Assertions need both. The runner joins them by `tool_use_id` so each call
records `(name, input, result_preview, is_error)`.

## Adding a scenario

A scenario is a single Python module that:
1. Defines `PROMPT` (the user-style task description).
2. Builds `ClaudeAgentOptions` with at minimum `cwd`, `allowed_tools`, and
   a `PostToolUse` hook.
3. Iterates `query(...)` to capture tool calls + final result.
4. Runs a list of `assert_*` functions, each raising `AssertionFailed` with
   a diagnostic message.

Reuse the `CapturedRun` dataclass + `make_post_tool_hook` helper from
`find_events.py` — those are the only shared pieces today. Keep each
scenario as a single file until at least 3 exist; only then refactor a
common runner.

## Assertion style

**Assert on tool-call patterns, not prose.** Final answers vary across
runs. Tool calls don't.

Good:
- "agent called LSP with operation=documentSymbol on path X, got result containing 'IntegrationEvent'"
- "no Grep or Bash calls"

Bad:
- "answer is exactly: 'Here are the events: ...'"
- "answer contains 137 event names" (BC changes upstream → flake)

Use loose keyword checks (`>=3 known event names`) for prose, with
generous tolerance.

## What's not here yet (deferred)

- pytest integration — runners are plain Python scripts for now
- fixture isolation — scenario currently mutates `test-al-project/src/`
  (the seeded `ApprovalsMgmtSubs.Codeunit.al` is committed)
- CI hookup — meant for nightly cron, not every PR (cost)
- session resume — each scenario is one-shot
- per-scenario model override — defaults to whatever Claude Code is using
