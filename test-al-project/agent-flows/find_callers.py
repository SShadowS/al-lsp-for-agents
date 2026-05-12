"""Agent-flow scenario: find subscribers of a local [IntegrationEvent]
via call hierarchy.

Two fixtures:
  src/Codeunits/LocalEventPublisher.Codeunit.al
    codeunit 50060 "Local Event Publisher"
    - publishes [IntegrationEvent] OnMyLocalEvent

  src/Codeunits/LocalEventSubscriber.Codeunit.al
    codeunit 50061 "Local Event Subscriber"
    - [EventSubscriber] HandleMyLocalEvent on OnMyLocalEvent

The agent must:
  1. Find OnMyLocalEvent (LSP documentSymbol on the publisher file)
  2. Use prepareCallHierarchy + incomingCalls on it
  3. Surface "HandleMyLocalEvent" as the subscriber

Validates al-call-hierarchy integration end-to-end. The wrapper's
call-hierarchy bridge must wire prepareCallHierarchy → incomingCalls
correctly for IntegrationEvent publishers (which are special-cased by
al-call-hierarchy's event-aware indexing in v0.8.0).

Run from repo root:
    python test-al-project/agent-flows/find_callers.py
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from _harness import (  # noqa: E402
    AssertionFailed,
    CapturedRun,
    assert_lsp_used,
    make_assert_minimal_denied_attempts,
    run_and_report,
    truncate,
)

PROMPT = (
    "In `src/Codeunits/LocalEventPublisher.Codeunit.al`, the local "
    "`OnMyLocalEvent` IntegrationEvent is published. Use the LSP call "
    "hierarchy tools (`prepareCallHierarchy` then `incomingCalls`) to find "
    "every procedure that SUBSCRIBES to `OnMyLocalEvent`. Return each "
    "subscriber's name and the file it lives in. LSP only — do not search "
    "the filesystem."
)


def assert_call_hierarchy_used(run: CapturedRun) -> None:
    """The scenario specifically tests call hierarchy. The agent must
    call prepareCallHierarchy (or incomingCalls directly)."""
    ch_ops = {"prepareCallHierarchy", "incomingCalls"}
    ch_calls = [c for c in run.lsp_calls() if c.input.get("operation") in ch_ops]
    if not ch_calls:
        ops_seen = sorted({c.input.get("operation", "?") for c in run.lsp_calls()})
        raise AssertionFailed(
            "Agent didn't use call hierarchy (prepareCallHierarchy / "
            f"incomingCalls). LSP ops seen: {ops_seen}"
        )


def assert_incoming_calls_found_subscriber(run: CapturedRun) -> None:
    """incomingCalls must have returned a non-empty result containing
    the subscriber's name. If the call hierarchy bridge to
    al-call-hierarchy is broken, the result will be empty even though
    the subscriber is clearly visible in another file."""
    incoming = run.lsp_calls_for("incomingCalls")
    if not incoming:
        # incomingCalls is the assertion target. prepareCallHierarchy
        # alone isn't enough — it just resolves the item to lift onto.
        raise AssertionFailed(
            "Agent invoked call hierarchy but never reached "
            "incomingCalls. prepareCallHierarchy alone doesn't validate "
            "the subscriber link."
        )
    for call in incoming:
        if call.is_error:
            continue
        if "HandleMyLocalEvent" in call.result:
            return
    raise AssertionFailed(
        "incomingCalls succeeded but did not surface HandleMyLocalEvent. "
        "Wrapper or al-call-hierarchy may not be wiring "
        "[EventSubscriber] callers into the call graph. "
        "Results: " + " | ".join(truncate(c.result, 200) for c in incoming)
    )


def assert_answer_names_subscriber(run: CapturedRun) -> None:
    """Last-line sanity check: the final answer should mention the
    subscriber's procedure name. Tolerates any phrasing — just looks
    for the literal identifier."""
    answer = run.joined_answer()
    if "HandleMyLocalEvent" not in answer:
        raise AssertionFailed(
            "Final answer doesn't mention HandleMyLocalEvent. "
            f"Excerpt: {truncate(answer, 300)}"
        )


ASSERTIONS = [
    ("LSP tool was used", assert_lsp_used),
    ("Agent did not thrash on denied tools", make_assert_minimal_denied_attempts()),
    ("Call hierarchy operations were used", assert_call_hierarchy_used),
    ("incomingCalls found the subscriber", assert_incoming_calls_found_subscriber),
    ("Final answer names HandleMyLocalEvent", assert_answer_names_subscriber),
]


if __name__ == "__main__":
    sys.exit(run_and_report("find_callers", PROMPT, ASSERTIONS))
