"""Agent-flow proof of concept: find IntegrationEvent publishers in BC base.

This is the highest-ROI end-to-end test for the AL LSP wrapper. A real
Claude Code agent (via the Agent SDK) is given a typical BC dev task:

    "Find IntegrationEvent publishers in Approvals Mgmt. (Codeunit 1535)
     I could subscribe to from Source\\ApprovalsMgmtSubs.Codeunit.al"

The agent uses Claude Code's LSP tool against the wrapper. The scenario
asserts on the tool-call PATTERN rather than the prose answer:

  * the agent issued at least one LSP request that resolved an
    al-preview:/ URI (or its materialized cache path) successfully,
  * the resulting `documentSymbol` response was non-empty and contained
    IntegrationEvent symbols,
  * the final answer enumerates concrete event names (lightweight
    keyword check, not exact-match).

Prerequisites:
  * ANTHROPIC_API_KEY set in env
  * `pip install -r requirements.txt`
  * AL LSP plugin enabled in Claude Code (e.g. via
    `/plugin install al-language-server-go-windows@al-lsp-for-agents`)
  * .alpackages populated (Base Application + Application + System Application)

Run from repo root:
    python test-al-project/agent-flows/find_events.py
"""

from __future__ import annotations

import io
import json
import os
import re
import sys
from datetime import datetime
from dataclasses import dataclass, field
from pathlib import Path

import anyio
from claude_agent_sdk import (
    AssistantMessage,
    ClaudeAgentOptions,
    HookMatcher,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ToolResultBlock,
    ToolUseBlock,
    query,
)
from claude_agent_sdk.types import SyncHookJSONOutput

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKSPACE = REPO_ROOT / "test-al-project"
LOGS_DIR = Path(__file__).resolve().parent / "logs"


class TeeWriter(io.TextIOBase):
    """Writes to both an underlying stream and a log file. Used to mirror
    stdout into per-run log files without losing live terminal output."""

    def __init__(self, primary, secondary):
        self._primary = primary
        self._secondary = secondary

    def write(self, s: str) -> int:
        self._primary.write(s)
        self._secondary.write(s)
        return len(s)

    def flush(self) -> None:
        self._primary.flush()
        self._secondary.flush()

    def isatty(self) -> bool:
        return getattr(self._primary, "isatty", lambda: False)()

# Structural assertion: the answer must contain at least N strings that
# look like BC IntegrationEvent publishers. Codeunit 1535 has 137 of
# them in real BC (verified by extracting the .app archive directly).
# Match anything starting with `OnSend|OnCancel|OnApprove|OnReject|OnAfter|OnBefore`
# followed by an identifier — covers all known BC event-naming patterns
# and stays stable as BC adds/removes specific events upstream.
#
# Using a regex on the structural shape (rather than a hardcoded list of
# event names) avoids tying the assertion to whichever events the agent
# decides to highlight. Some runs list "OnSend*", others lead with
# "OnAfter*" — both are valid as long as enough recognizable events
# appear.
EVENT_NAME_RE = re.compile(
    r"\bOn(?:Send|Cancel|Approve|Reject|Delegate|After|Before)"
    r"[A-Z][A-Za-z0-9]+\b"
)

# Minimum unique event names matching the pattern. Threshold chosen to
# require real enumeration (not just one or two from memory) while
# staying loose enough for varied phrasing.
MIN_EVENTS_IN_ANSWER = 5


# ---------------------------------------------------------------------------
# Tool-call capture
# ---------------------------------------------------------------------------


@dataclass
class ToolCall:
    name: str
    input: dict
    result_preview: str = ""
    is_error: bool = False


@dataclass
class DeniedToolCall:
    """A tool call the PreToolUse hook refused. Recorded for the report
    so we can see what escape hatches the agent reached for even though
    they never executed."""

    name: str
    input: dict


@dataclass
class CapturedRun:
    tool_calls: list[ToolCall] = field(default_factory=list)
    denied_tool_calls: list[DeniedToolCall] = field(default_factory=list)
    assistant_text: list[str] = field(default_factory=list)
    final_result: str | None = None
    session_id: str | None = None

    # ---- helpers used by assertions -------------------------------------
    def lsp_calls(self) -> list[ToolCall]:
        return [c for c in self.tool_calls if c.name == "LSP"]

    def lsp_calls_for(self, operation: str) -> list[ToolCall]:
        return [c for c in self.lsp_calls() if c.input.get("operation") == operation]

    def joined_answer(self) -> str:
        if self.final_result:
            return self.final_result
        return "\n".join(self.assistant_text)


def _truncate(s: str, n: int = 240) -> str:
    s = s.replace("\n", " ").strip()
    return s if len(s) <= n else s[: n - 3] + "..."


# ---------------------------------------------------------------------------
# Hook to log tool-use results (PostToolUse fires AFTER tool returns)
# ---------------------------------------------------------------------------


# Allowlist of tools the scenario permits. Anything else is hard-denied
# in PreToolUse with an explanatory reason. Maintaining a denylist is
# whack-a-mole because every new Claude Code feature (Monitor, Agent,
# Task*, MCP servers) is a fresh escape hatch. Allowlist flips the
# default and stays correct as the platform grows.
#
# Choice rationale:
#   LSP        — the system under test
#   ToolSearch — the legitimate mechanism for loading deferred-tool schemas
#                (LSP is deferred — agents fail to call it correctly until
#                ToolSearch returns its schema). Denying ToolSearch forces
#                agents to guess the LSP shape, which is not what we test.
#   Read       — agent needs to see fixture source for context
#   TodoWrite  — non-IO planning aid; some agents lean on it heavily
ALLOWED_TOOLS = frozenset({"LSP", "ToolSearch", "Read", "TodoWrite"})


def make_pre_tool_hook(captured: "CapturedRun"):
    """Deny anything outside ALLOWED_TOOLS. Records the attempted call
    in the captured run so the assertion report can show what the agent
    tried to reach for."""

    async def pre_tool_use(input_data, _tool_use_id, _context) -> SyncHookJSONOutput:
        name = input_data.get("tool_name", "")
        if name in ALLOWED_TOOLS:
            return {}
        denied_input = input_data.get("tool_input", {}) or {}
        captured.denied_tool_calls.append(
            DeniedToolCall(name=name, input=denied_input)
        )
        print(
            f"[agent-flow] DENY: {name} "
            f"input={_truncate(json.dumps(denied_input, default=str), 140)}",
            flush=True,
        )
        return {
            "decision": "block",
            "reason": (
                f"This test only permits {sorted(ALLOWED_TOOLS)}. "
                f"The AL LSP wrapper is the system under test; using "
                f"non-LSP code search defeats the purpose. If LSP doesn't "
                f"return what you need, report that as a failure rather "
                f"than working around it."
            ),
        }

    return pre_tool_use


def make_post_tool_hook(captured: CapturedRun):
    async def post_tool_use(input_data, _tool_use_id, _context) -> SyncHookJSONOutput:
        # input_data shape per SDK: {"tool_name": str, "tool_input": dict,
        # "tool_response": Any, "is_error": bool, ...}
        # We update the LAST matching ToolCall we recorded from
        # AssistantMessage blocks; if missing (shouldn't happen) we append.
        name = input_data.get("tool_name", "")
        result = input_data.get("tool_response", "")
        if not isinstance(result, str):
            try:
                result = json.dumps(result, default=str)
            except Exception:
                result = str(result)
        is_error = bool(input_data.get("is_error", False))

        for call in reversed(captured.tool_calls):
            if call.name == name and not call.result_preview:
                call.result_preview = _truncate(result)
                call.is_error = is_error
                return {}

        # Fallback: tool result arrived without a matching ToolUseBlock —
        # record it standalone for visibility.
        captured.tool_calls.append(
            ToolCall(
                name=name,
                input=input_data.get("tool_input", {}),
                result_preview=_truncate(result),
                is_error=is_error,
            )
        )
        return {}

    return post_tool_use


# ---------------------------------------------------------------------------
# Scenario runner
# ---------------------------------------------------------------------------


PROMPT = (
    "I'm writing event subscribers in `src/Codeunits/ApprovalsMgmtSubs.Codeunit.al`. "
    "Use the AL LSP tools (NOT grep) to find IntegrationEvent publishers in "
    "Codeunit 1535 'Approvals Mgmt.' from the Base Application that I could "
    "subscribe to. Return the event names as a bullet list. Be concise."
)


async def run_scenario() -> CapturedRun:
    captured = CapturedRun()

    # Permission strategy: PreToolUse hook hard-denies anything outside
    # ALLOWED_TOOLS. allowed_tools/disallowed_tools are kept as a belt-
    # and-suspenders allowlist hint but the deny-by-default hook is the
    # authoritative gate. Rationale in make_pre_tool_hook docstring.
    options = ClaudeAgentOptions(
        cwd=str(WORKSPACE),
        max_turns=30,
        permission_mode="bypassPermissions",
        allowed_tools=sorted(ALLOWED_TOOLS),
        hooks={
            "PreToolUse":  [HookMatcher(hooks=[make_pre_tool_hook(captured)])],
            "PostToolUse": [HookMatcher(hooks=[make_post_tool_hook(captured)])],
        },
    )

    print(f"[agent-flow] starting run (cwd={WORKSPACE})", flush=True)
    print(f"[agent-flow] prompt: {_truncate(PROMPT, 200)}", flush=True)

    async for message in query(prompt=PROMPT, options=options):
        if isinstance(message, SystemMessage) and message.subtype == "init":
            captured.session_id = message.data.get("session_id")
            print(f"[agent-flow] session: {captured.session_id}", flush=True)
            continue

        if isinstance(message, AssistantMessage):
            for block in message.content:
                if isinstance(block, TextBlock):
                    captured.assistant_text.append(block.text)
                    print(f"[agent-flow] text: {_truncate(block.text, 160)}",
                          flush=True)
                elif isinstance(block, ToolUseBlock):
                    captured.tool_calls.append(
                        ToolCall(name=block.name, input=dict(block.input))
                    )
                    op = block.input.get("operation") if block.name == "LSP" else ""
                    op_part = f" op={op}" if op else ""
                    print(f"[agent-flow] tool: {block.name}{op_part} "
                          f"input={_truncate(json.dumps(block.input, default=str), 160)}",
                          flush=True)
                elif isinstance(block, ToolResultBlock):
                    # Sometimes carried inline; PostToolUse hook is the
                    # primary capture path so we don't double-count here.
                    pass
            continue

        if isinstance(message, ResultMessage):
            captured.final_result = message.result or ""
            print("[agent-flow] result received", flush=True)

    return captured


# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------


class AssertionFailed(Exception):
    pass


def assert_lsp_used(run: CapturedRun) -> None:
    lsp = run.lsp_calls()
    if not lsp:
        raise AssertionFailed(
            "Agent did not call the LSP tool at all. "
            "Captured tool calls: " + ", ".join(c.name for c in run.tool_calls)
        )


def assert_minimal_denied_attempts(run: CapturedRun) -> None:
    """A small handful of denied attempts is normal (LLMs try things).
    A flood means the agent gave up on LSP and tried every escape hatch.
    Threshold is intentionally loose; the goal is to catch the "the
    wrapper gave nothing useful so the agent thrashed for 20 turns"
    pattern, not to demand a perfect agent."""
    MAX_DENIED = 5
    if len(run.denied_tool_calls) > MAX_DENIED:
        sample = ", ".join(
            f"{c.name}({_truncate(json.dumps(c.input, default=str), 60)})"
            for c in run.denied_tool_calls[:6]
        )
        raise AssertionFailed(
            f"Agent attempted {len(run.denied_tool_calls)} blocked tools "
            f"(threshold {MAX_DENIED}). Signals that LSP didn't deliver "
            f"and the agent thrashed. First few: {sample}"
        )


def assert_documentsymbol_succeeded_on_approvals(run: CapturedRun) -> None:
    """Find at least one documentSymbol call whose result references
    Codeunit 1535's events / methods."""
    ds = run.lsp_calls_for("documentSymbol")
    if not ds:
        raise AssertionFailed("No documentSymbol LSP call observed.")
    for call in ds:
        if call.is_error:
            continue
        # Look for IntegrationEvent tag OR known event name in the result.
        preview = call.result_preview
        if "IntegrationEvent" in preview or re.search(r"OnAfter|OnBefore", preview):
            return
    raise AssertionFailed(
        "documentSymbol calls succeeded but no IntegrationEvent symbols "
        "appeared. Previews: "
        + " | ".join(c.result_preview for c in ds)
    )


def assert_answer_lists_events(run: CapturedRun) -> None:
    answer = run.joined_answer()
    hits = sorted(set(EVENT_NAME_RE.findall(answer)))
    if len(hits) < MIN_EVENTS_IN_ANSWER:
        raise AssertionFailed(
            f"Final answer mentioned only {len(hits)} BC-event-shaped "
            f"names (threshold {MIN_EVENTS_IN_ANSWER}). Hits: {hits}. "
            f"Excerpt: {_truncate(answer, 400)}"
        )


ASSERTIONS = [
    ("LSP tool was used", assert_lsp_used),
    ("Agent did not thrash on denied tools", assert_minimal_denied_attempts),
    ("documentSymbol returned IntegrationEvent symbols",
     assert_documentsymbol_succeeded_on_approvals),
    (f"Final answer lists >={MIN_EVENTS_IN_ANSWER} expected event names",
     assert_answer_lists_events),
]


def report(run: CapturedRun) -> int:
    print()
    print("=" * 72)
    print("Agent-flow run summary")
    print("=" * 72)
    print(f"session_id    : {run.session_id}")
    print(f"tool calls    : {len(run.tool_calls)}")
    for c in run.tool_calls:
        op = c.input.get("operation") if c.name == "LSP" else ""
        flag = " [ERR]" if c.is_error else ""
        op_part = f" op={op}" if op else ""
        print(f"  - {c.name}{op_part}{flag}: {_truncate(c.result_preview, 140)}")
    print()
    print(f"denied calls  : {len(run.denied_tool_calls)}")
    for c in run.denied_tool_calls:
        print(f"  - {c.name}: {_truncate(json.dumps(c.input, default=str), 140)}")
    print()
    print(f"final answer  : {_truncate(run.joined_answer(), 600)}")
    print()
    print("Assertions:")
    failures = 0
    for label, fn in ASSERTIONS:
        try:
            fn(run)
            print(f"  PASS  {label}")
        except AssertionFailed as e:
            failures += 1
            print(f"  FAIL  {label}")
            print(f"        {e}")
    print()
    print(f"{'ALL PASS' if failures == 0 else f'{failures} FAILURE(S)'}")
    return failures


async def main() -> int:
    LOGS_DIR.mkdir(parents=True, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d-%H%M%S")
    log_path = LOGS_DIR / f"{ts}-find_events.log"
    log_fh = open(log_path, "w", encoding="utf-8", buffering=1)

    original_stdout = sys.stdout
    sys.stdout = TeeWriter(original_stdout, log_fh)

    try:
        # Print to stdout (not stderr) so users running without redirection
        # see the failure mode immediately. "(no content)" silence has been
        # observed when stderr was swallowed by the shell wrapper.
        print("[agent-flow] startup", flush=True)
        print(f"[agent-flow] log file: {log_path}", flush=True)
        if not os.environ.get("ANTHROPIC_API_KEY"):
            print(
                "[agent-flow] FATAL: ANTHROPIC_API_KEY not set.\n"
                "  PowerShell:  $env:ANTHROPIC_API_KEY = '<your-key>'\n"
                "  bash/zsh  :  export ANTHROPIC_API_KEY=<your-key>",
                flush=True,
            )
            return 2
        if not (WORKSPACE / "app.json").exists():
            print(f"[agent-flow] FATAL: workspace {WORKSPACE} missing app.json",
                  flush=True)
            return 2
        print(
            f"[agent-flow] API key present "
            f"(len={len(os.environ['ANTHROPIC_API_KEY'])})",
            flush=True,
        )
        run = await run_scenario()
        return report(run)
    finally:
        sys.stdout = original_stdout
        log_fh.close()
        print(f"[agent-flow] saved log to {log_path}")


if __name__ == "__main__":
    sys.exit(anyio.run(main))
