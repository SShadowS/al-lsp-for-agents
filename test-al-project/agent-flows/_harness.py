"""Shared infrastructure for agent-flow scenarios.

A scenario file should be small: define the prompt, the assertions, and
call `run_and_report()`. Everything else — SDK plumbing, tool-call
capture, deny-by-default hooks, log file tee'ing, report rendering —
lives here so it stays consistent across scenarios.

Design choices worth preserving across scenarios:

  * **PreToolUse deny-by-default**. allowed_tools auto-approves but does
    NOT restrict, and disallowed_tools is a maintenance treadmill (every
    new Claude Code feature is a fresh escape hatch). The PreToolUse
    hook blocks anything outside ALLOWED_TOOLS with an explanatory
    reason. After this lockdown, an assertion failure is a real wrapper
    bug, not agent improvisation.

  * **Tool-call patterns over prose**. Final-answer text varies across
    runs; tool calls don't. Assertions should inspect captured tool
    calls + their result previews. Loose regex / token checks for the
    final answer are okay as a final sanity check, but the primary
    signal lives in `run.tool_calls`.

  * **Per-run log file** mirrored from stdout via TeeWriter. Each run
    leaves a self-contained log under `logs/<timestamp>-<scenario>.log`
    even when the script crashes (anyio re-raises, max_turns reached,
    etc.). The log is the artifact future-you reads to figure out why
    the agent did what it did.
"""

from __future__ import annotations

import io
import json
import os
import sys
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Awaitable, Callable

import anyio
from claude_agent_sdk import (
    AssistantMessage,
    ClaudeAgentOptions,
    HookMatcher,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ToolUseBlock,
    query,
)
from claude_agent_sdk.types import SyncHookJSONOutput

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKSPACE = REPO_ROOT / "test-al-project"
LOGS_DIR = Path(__file__).resolve().parent / "logs"

# Tools the harness permits. Anything else is hard-denied in PreToolUse
# with an explanatory reason. Scenarios may extend this set; the
# default is the minimal LSP-only sandbox.
#
#   LSP        — the system under test
#   ToolSearch — loads deferred-tool schemas (LSP is deferred)
#   Read       — agent needs fixture source for context
#   TodoWrite  — non-IO planning aid; some agents rely on it
DEFAULT_ALLOWED_TOOLS = frozenset({"LSP", "ToolSearch", "Read", "TodoWrite"})


# ---------------------------------------------------------------------------
# Tee writer + capture dataclasses
# ---------------------------------------------------------------------------


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


@dataclass
class ToolCall:
    name: str
    input: dict
    # Full untruncated result. Assertions inspect this. Can be large
    # (zips of JSON, multi-screen incomingCalls dumps). The report
    # uses result_preview for display.
    result: str = ""
    # 240-char truncated form, used for the streaming log + report.
    # Don't grep this in assertions — it WILL chop strings you care
    # about at the worst possible byte.
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


def truncate(s: str, n: int = 240) -> str:
    s = s.replace("\n", " ").strip()
    return s if len(s) <= n else s[: n - 3] + "..."


# ---------------------------------------------------------------------------
# Hooks
# ---------------------------------------------------------------------------


def make_pre_tool_hook(captured: CapturedRun, allowed: frozenset[str]):
    """Deny anything outside `allowed`. Records the attempted call in
    the captured run so the assertion report can show what the agent
    tried to reach for."""

    async def pre_tool_use(input_data, _tool_use_id, _context) -> SyncHookJSONOutput:
        name = input_data.get("tool_name", "")
        if name in allowed:
            return {}
        denied_input = input_data.get("tool_input", {}) or {}
        captured.denied_tool_calls.append(
            DeniedToolCall(name=name, input=denied_input)
        )
        print(
            f"[agent-flow] DENY: {name} "
            f"input={truncate(json.dumps(denied_input, default=str), 140)}",
            flush=True,
        )
        return {
            "decision": "block",
            "reason": (
                f"This test only permits {sorted(allowed)}. "
                f"The AL LSP wrapper is the system under test; using "
                f"non-LSP code search defeats the purpose. If LSP doesn't "
                f"return what you need, report that as a failure rather "
                f"than working around it."
            ),
        }

    return pre_tool_use


def make_post_tool_hook(captured: CapturedRun):
    """Joins tool results back to the ToolUseBlock the agent emitted.
    The block carries name + input; PostToolUse adds result_preview +
    is_error. Assertions need both, so we join by tool_use_id position
    (last-matching name)."""

    async def post_tool_use(input_data, _tool_use_id, _context) -> SyncHookJSONOutput:
        name = input_data.get("tool_name", "")
        result = input_data.get("tool_response", "")
        if not isinstance(result, str):
            try:
                result = json.dumps(result, default=str)
            except Exception:
                result = str(result)
        is_error = bool(input_data.get("is_error", False))

        for call in reversed(captured.tool_calls):
            if call.name == name and not call.result:
                call.result = result
                call.result_preview = truncate(result)
                call.is_error = is_error
                return {}

        # Fallback: tool result arrived without a matching ToolUseBlock.
        captured.tool_calls.append(
            ToolCall(
                name=name,
                input=input_data.get("tool_input", {}),
                result=result,
                result_preview=truncate(result),
                is_error=is_error,
            )
        )
        return {}

    return post_tool_use


# ---------------------------------------------------------------------------
# Scenario driver
# ---------------------------------------------------------------------------


async def run_scenario(
    prompt: str,
    *,
    workspace: Path = WORKSPACE,
    allowed_tools: frozenset[str] = DEFAULT_ALLOWED_TOOLS,
    max_turns: int = 30,
) -> CapturedRun:
    """Run the agent once with the given prompt and capture every tool
    call + assistant message. Streams progress to stdout so a live
    operator (or `tail -f` on the log) can follow the run."""

    captured = CapturedRun()

    options = ClaudeAgentOptions(
        cwd=str(workspace),
        max_turns=max_turns,
        permission_mode="bypassPermissions",
        allowed_tools=sorted(allowed_tools),
        hooks={
            "PreToolUse":  [HookMatcher(hooks=[make_pre_tool_hook(captured, allowed_tools)])],
            "PostToolUse": [HookMatcher(hooks=[make_post_tool_hook(captured)])],
        },
    )

    print(f"[agent-flow] starting run (cwd={workspace})", flush=True)
    print(f"[agent-flow] prompt: {truncate(prompt, 200)}", flush=True)

    async for message in query(prompt=prompt, options=options):
        if isinstance(message, SystemMessage) and message.subtype == "init":
            captured.session_id = message.data.get("session_id")
            print(f"[agent-flow] session: {captured.session_id}", flush=True)
            continue

        if isinstance(message, AssistantMessage):
            for block in message.content:
                if isinstance(block, TextBlock):
                    captured.assistant_text.append(block.text)
                    print(f"[agent-flow] text: {truncate(block.text, 160)}",
                          flush=True)
                elif isinstance(block, ToolUseBlock):
                    captured.tool_calls.append(
                        ToolCall(name=block.name, input=dict(block.input))
                    )
                    op = block.input.get("operation") if block.name == "LSP" else ""
                    op_part = f" op={op}" if op else ""
                    print(
                        f"[agent-flow] tool: {block.name}{op_part} "
                        f"input={truncate(json.dumps(block.input, default=str), 160)}",
                        flush=True,
                    )
            continue

        if isinstance(message, ResultMessage):
            captured.final_result = message.result or ""
            print("[agent-flow] result received", flush=True)

    return captured


# ---------------------------------------------------------------------------
# Assertion runner
# ---------------------------------------------------------------------------


class AssertionFailed(Exception):
    pass


# A scenario assertion is a function that raises AssertionFailed with a
# diagnostic message when its expectation is not met. The label is shown
# in the PASS/FAIL line. List ordering is the report ordering.
Assertion = Callable[[CapturedRun], None]


# Shared assertions every scenario probably wants. Scenarios pick which
# to include in their ASSERTIONS list and add scenario-specific ones.


def assert_lsp_used(run: CapturedRun) -> None:
    lsp = run.lsp_calls()
    if not lsp:
        raise AssertionFailed(
            "Agent did not call the LSP tool at all. Captured: "
            + ", ".join(c.name for c in run.tool_calls)
        )


def make_assert_minimal_denied_attempts(max_denied: int = 5) -> Assertion:
    def assert_minimal_denied_attempts(run: CapturedRun) -> None:
        if len(run.denied_tool_calls) > max_denied:
            sample = ", ".join(
                f"{c.name}({truncate(json.dumps(c.input, default=str), 60)})"
                for c in run.denied_tool_calls[:6]
            )
            raise AssertionFailed(
                f"Agent attempted {len(run.denied_tool_calls)} blocked tools "
                f"(threshold {max_denied}). Signals LSP didn't deliver and "
                f"the agent thrashed. First few: {sample}"
            )

    return assert_minimal_denied_attempts


# ---------------------------------------------------------------------------
# Report rendering
# ---------------------------------------------------------------------------


def report(run: CapturedRun, assertions: list[tuple[str, Assertion]]) -> int:
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
        print(f"  - {c.name}{op_part}{flag}: {truncate(c.result_preview, 140)}")
    print()
    print(f"denied calls  : {len(run.denied_tool_calls)}")
    for c in run.denied_tool_calls:
        print(f"  - {c.name}: {truncate(json.dumps(c.input, default=str), 140)}")
    print()
    print(f"final answer  : {truncate(run.joined_answer(), 600)}")
    print()
    print("Assertions:")
    failures = 0
    for label, fn in assertions:
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


# ---------------------------------------------------------------------------
# Main entrypoint helper
# ---------------------------------------------------------------------------


def run_and_report(
    scenario_name: str,
    prompt: str,
    assertions: list[tuple[str, Assertion]],
    *,
    allowed_tools: frozenset[str] = DEFAULT_ALLOWED_TOOLS,
    max_turns: int = 30,
) -> int:
    """Top-level entrypoint a scenario can call from `if __name__ ==
    '__main__'`. Handles env-var validation, per-run log tee, the async
    runner, and final report. Returns exit code."""

    async def _main() -> int:
        LOGS_DIR.mkdir(parents=True, exist_ok=True)
        ts = datetime.now().strftime("%Y%m%d-%H%M%S")
        log_path = LOGS_DIR / f"{ts}-{scenario_name}.log"
        log_fh = open(log_path, "w", encoding="utf-8", buffering=1)

        original_stdout = sys.stdout
        sys.stdout = TeeWriter(original_stdout, log_fh)

        try:
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
                print(
                    f"[agent-flow] FATAL: workspace {WORKSPACE} missing app.json",
                    flush=True,
                )
                return 2
            print(
                f"[agent-flow] API key present "
                f"(len={len(os.environ['ANTHROPIC_API_KEY'])})",
                flush=True,
            )
            run = await run_scenario(
                prompt,
                allowed_tools=allowed_tools,
                max_turns=max_turns,
            )
            return report(run, assertions)
        finally:
            sys.stdout = original_stdout
            log_fh.close()
            print(f"[agent-flow] saved log to {log_path}")

    return anyio.run(_main)
