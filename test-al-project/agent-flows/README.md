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

Shared infrastructure lives in `_harness.py`. A scenario is a small
Python module that defines:

- `PROMPT` — the user-style task description the agent runs against.
- One or more assertion functions (each raising `AssertionFailed` on
  failure), and an `ASSERTIONS` list `[(label, fn), ...]`.
- `if __name__ == "__main__": sys.exit(run_and_report(name, PROMPT, ASSERTIONS))`.

Two assertions are usually included from `_harness`:
- `assert_lsp_used` — the LSP tool was called at all.
- `make_assert_minimal_denied_attempts()` — under N escape-hatch
  attempts (default 5). High counts signal the agent thrashed because
  LSP failed to deliver.

The harness ships with a `PreToolUse` deny hook that hard-blocks any
tool not in `DEFAULT_ALLOWED_TOOLS = {LSP, ToolSearch, Read, TodoWrite}`.
Scenarios can extend with `allowed_tools=frozenset({..., "Bash"})` etc.,
but the deny-by-default keeps the test honest.

## Current scenarios

| Scenario | Validates |
|---|---|
| `find_events.py` | al-preview URI rewrite + content-scan + canonical filename match (v1.11.2). Agent enumerates IntegrationEvents in Base App's Codeunit 1535. |
| `subscribe_to_event.py` | `enrichEventReferenceHover` overlay (v1.11.0 Phase B). Agent hovers on event-name literal inside `[EventSubscriber(...)]`. |
| `find_callers.py` | al-call-hierarchy integration. Agent uses `incomingCalls` to find subscribers of a local IntegrationEvent. |

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
